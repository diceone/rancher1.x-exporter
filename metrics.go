package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	// Used to prepand Prometheus metrics created by this exporter.
	namespace         = "rancher"
	genericobjectKind = "rancherMetrics"
)

var (
	/**
		InfinityWorks
	 */

	agentStates   = []string{"activating", "active", "reconnecting", "disconnected", "disconnecting", "finishing-reconnect", "reconnected"}
	hostStates    = []string{"activating", "active", "deactivating", "error", "erroring", "inactive", "provisioned", "purged", "purging", "registering", "removed", "removing", "requested", "restoring", "updating_active", "updating_inactive"}
	stackStates   = []string{"activating", "active", "canceled_upgrade", "canceling_upgrade", "error", "erroring", "finishing_upgrade", "removed", "removing", "requested", "restarting", "rolling_back", "updating_active", "upgraded", "upgrading"}
	serviceStates = []string{"activating", "active", "canceled_upgrade", "canceling_upgrade", "deactivating", "finishing_upgrade", "inactive", "registering", "removed", "removing", "requested", "restarting", "rolling_back", "updating_active", "updating_inactive", "upgraded", "upgrading"}
	healthStates  = []string{"healthy", "unhealthy"}

	// health & state of host, stack, service
	infinityWorksHostsState = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "host_state",
			Help:      "State of defined host as reported by the Rancher API",
		}, []string{"id", "name", "state"})

	infinityWorksHostAgentsState = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "host_agent_state",
			Help:      "State of defined host agent as reported by the Rancher API",
		}, []string{"id", "name", "state"})

	infinityWorksStacksHealth = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "stack_health_status",
			Help:      "HealthState of defined stack as reported by Rancher",
		}, []string{"id", "name", "health_state", "system"})

	infinityWorksStacksState = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "stack_state",
			Help:      "State of defined stack as reported by Rancher",
		}, []string{"id", "name", "state", "system"})

	infinityWorksServicesScale = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "service_scale",
			Help:      "scale of defined service as reported by Rancher",
		}, []string{"name", "stack_name", "system"})

	infinityWorksServicesHealth = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "service_health_status",
			Help:      "HealthState of the service, as reported by the Rancher API",
		}, []string{"id", "stack_id", "name", "stack_name", "health_state", "system"})

	infinityWorksServicesState = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "service_state",
			Help:      "State of the service, as reported by the Rancher API",
		}, []string{"id", "stack_id", "name", "stack_name", "state", "system"})

	/**
		Extended
	 */

	// total counter of stack, service, instance
	extendingTotalStackBootstrap = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "stack_bootstrap_total",
		Help:      "Current total number of the started stacks in Rancher",
	}, []string{"environment_name", "name", "system", "type"})

	extendingTotalStackFailure = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "stack_failure_total",
		Help:      "Current total number of the failure stacks in Rancher",
	}, []string{"environment_name", "name", "system", "type"})

	extendingTotalServiceBootstrap = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "service_bootstrap_total",
		Help:      "Current total number of the started services in Rancher",
	}, []string{"environment_name", "stack_name", "name", "system", "type"})

	extendingTotalServiceFailure = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "service_failure_total",
		Help:      "Current total number of the failure services in Rancher",
	}, []string{"environment_name", "stack_name", "name", "system", "type"})

	extendingTotalInstanceBootstrap = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "instance_bootstrap_total",
		Help:      "Current total number of the started containers in Rancher",
	}, []string{"environment_name", "stack_name", "service_name", "name", "system", "type"})

	extendingTotalInstanceFailure = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "instance_failure_total",
		Help:      "Current total number of the failure containers in Rancher",
	}, []string{"environment_name", "stack_name", "service_name", "name", "system", "type"})

	// startup gauge
	extendingInstanceBootstrapMsCost = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "instance_startup_ms",
		Help:      "The startup milliseconds of instances in Rancher",
	}, []string{"environment_name", "stack_name", "service_name", "name", "system", "type"})

	// heartbeat
	extendingStackHeartbeat = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "stack_heartbeat",
		Help:      "The heartbeat of stacks in Rancher",
	}, []string{"environment_name", "name", "system", "type"})

	extendingServiceHeartbeat = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "service_heartbeat",
		Help:      "The heartbeat of services in Rancher",
	}, []string{"environment_name", "stack_name", "name", "system", "type"})

	extendingInstanceHeartbeat = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "instance_heartbeat",
		Help:      "The heartbeat of instances in Rancher",
	}, []string{"environment_name", "stack_name", "service_name", "name", "system", "type"})
)

/**
	static
 */
func newRancherClient(timeoutSeconds time.Duration) *rancherClient {
	return &rancherClient{
		&http.Client{Timeout: timeoutSeconds},
	}
}

func newMetric() *metric {
	m := &metric{
		m:        &sync.RWMutex{},
		Projects: make(map[string]project, 10),
	}

	return m
}

/**
	rancherClient class
 */
type rancherClient struct {
	client *http.Client
}

func (r *rancherClient) get(url string) *target {
	var t target
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Error(err)
	}

	req.SetBasicAuth(cattleAccessKey, cattleSecretKey)
	resp, err := r.client.Do(req)
	if err != nil {
		log.Error(err)
	}
	defer resp.Body.Close()

	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		log.Error(err)
	}

	return &t
}

func (r *rancherClient) post(url string, body io.Reader) (int, error) {
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return 0, err
	}

	req.SetBasicAuth(cattleAccessKey, cattleSecretKey)
	resp, err := r.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	return resp.StatusCode, nil
}

/**
	target class
 */
type targetData struct {
	HealthState    string   `json:"healthState,omitempty"`
	Key            string   `json:"key,omitempty"`
	Name           string   `json:"name,omitempty"`
	State          string   `json:"state,omitempty"`
	System         bool     `json:"system,omitempty"`
	Scale          int      `json:"scale,omitempty"`
	HostName       string   `json:"hostname,omitempty"`
	ID             string   `json:"id,omitempty"`
	StackID        string   `json:"stackId,omitempty"`
	EnvID          string   `json:"environmentId,omitempty"`
	Type           string   `json:"type,omitempty"`
	AgentState     string   `json:"agentState,omitempty"`
	CreatedTS      uint64   `json:"createdTS,omitempty"`
	FirstRunningTS uint64   `json:"firstRunningTS,omitempty"`
	ResourceData   *project `json:"resourceData,omitempty"`
}

type targetPagination struct {
	Next string `json:"next,omitempty"`
}

type target struct {
	Data []*targetData `json:"data"`

	Pagination *targetPagination `json:"pagination"`
}

/**
	object class
 */
type object struct {
	Id    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
	Type  string `json:"type"`

	BootstrapCount uint64 `json:"bootstrapCount"`
	FailureCount   uint64 `json:"failureCount"`
}

/**
	instance class
 */
type instance struct {
	*object
	System      bool   `json:"system"`
	StartupTime uint64 `json:"startupTime"`
	parent      *service
}

/**
	services class
 */
type service struct {
	*object
	Instances map[string]instance `json:"instances"`
	System    bool                `json:"system"`
	parent    *stack
}

func (o *service) fetch(ctx context.Context, rancherClient *rancherClient) {
	defer func() {
		if err := recover(); err != nil {
			log.Error(err)
		}
	}()

	if len(o.Id) == 0 {
		return
	}

	log.Debugln(">>> fetch instances on service:", o.Name, "on stack:", o.parent.Name, "on project:", o.parent.parent.Name)

	url := cattleURL + "/services/" + o.Id + "/instances?limit=100&sort=id"

	for {
		t := rancherClient.get(url)

		for _, d := range t.Data {
			var (
				instanceState  = d.State
				instanceId     = d.ID
				instanceName   = d.Name
				instanceSystem = strconv.FormatBool(d.System)
				instanceType   = d.Type

				serviceName = o.Name
				stackName   = o.parent.Name
				envName     = o.parent.parent.Name

				instanceStartupTime uint64 = 0
			)

			// Extended metrics
			extendingInstanceHeartbeat.WithLabelValues(envName, stackName, serviceName, instanceName, instanceSystem, instanceType).Set(float64(1))

			if take, ok := o.Instances[instanceName]; ok {
				if take.State != instanceState {
					switch instanceState {
					case "running":
						// get startupTime when instance is running
						if d.FirstRunningTS != 0 {
							instanceStartupTime = d.FirstRunningTS - d.CreatedTS
							extendingInstanceBootstrapMsCost.WithLabelValues(envName, stackName, serviceName, instanceName, instanceSystem, instanceType).Set(float64(instanceStartupTime))
						}
						take.StartupTime = instanceStartupTime

						extendingTotalInstanceBootstrap.WithLabelValues(envName, stackName, serviceName, instanceName, instanceSystem, instanceType).Inc()
						take.BootstrapCount += 1
					case "error":
						extendingTotalInstanceBootstrap.WithLabelValues(envName, stackName, serviceName, instanceName, instanceSystem, instanceType).Inc()
						take.BootstrapCount += 1

						extendingTotalInstanceFailure.WithLabelValues(envName, stackName, serviceName, instanceName, instanceSystem, instanceType).Inc()
						take.FailureCount += 1
					}
				}

				take.Id = instanceId
				take.Type = instanceType
				take.State = instanceState
				take.System = d.System
			} else {
				bootstrapCount, failureCount := uint64(0), uint64(0)

				switch instanceState {
				case "error":
					extendingTotalInstanceBootstrap.WithLabelValues(envName, stackName, serviceName, instanceName, instanceSystem, instanceType).Inc()
					bootstrapCount = 1

					extendingTotalInstanceFailure.WithLabelValues(envName, stackName, serviceName, instanceName, instanceSystem, instanceType).Inc()
					failureCount = 1
				case "stopped":
					fallthrough
				case "running":
					if d.FirstRunningTS != 0 {
						instanceStartupTime = d.FirstRunningTS - d.CreatedTS
						extendingInstanceBootstrapMsCost.WithLabelValues(envName, stackName, serviceName, instanceName, instanceSystem, instanceType).Set(float64(instanceStartupTime))
					}

					extendingTotalInstanceBootstrap.WithLabelValues(envName, stackName, serviceName, instanceName, instanceSystem, instanceType).Inc()
					bootstrapCount = 1

					extendingTotalInstanceFailure.WithLabelValues(envName, stackName, serviceName, instanceName, instanceSystem, instanceType)
				default:
					extendingTotalInstanceBootstrap.WithLabelValues(envName, stackName, serviceName, instanceName, instanceSystem, instanceType)
					extendingTotalInstanceFailure.WithLabelValues(envName, stackName, serviceName, instanceName, instanceSystem, instanceType)
				}

				o.Instances[instanceName] = instance{
					object: &object{
						Id:             instanceId,
						Name:           instanceName,
						State:          instanceState,
						Type:           instanceType,
						BootstrapCount: bootstrapCount,
						FailureCount:   failureCount,
					},
					System:      d.System,
					StartupTime: instanceStartupTime,
					parent:      o,
				}
			}
		}

		if len(t.Pagination.Next) != 0 {
			url = t.Pagination.Next
		} else {
			break
		}
	}

	log.Debugln("<<< fetch instances on service:", o.Name, "on stack:", o.parent.Name, "on project:", o.parent.parent.Name)
}

/**
	stack class
 */
type stack struct {
	*object
	Services map[string]service `json:"services"`
	System   bool               `json:"system"`
	parent   *project
}

func (o *stack) fetch(ctx context.Context, rancherClient *rancherClient) {
	defer func() {
		if err := recover(); err != nil {
			log.Error(err)
		}
	}()

	if len(o.Id) == 0 {
		return
	}

	log.Debugln(">> fetch service on stack:", o.Name, "on project:", o.parent.Name)

	var url string
	if hideSys {
		url = cattleURL + "/stacks/" + o.Id + "/services?limit=100&sort=id&system=false"
	} else {
		url = cattleURL + "/stacks/" + o.Id + "/services?limit=100&sort=id"
	}

	for {
		t := rancherClient.get(url)

		for _, d := range t.Data {
			var (
				serviceHealthState = d.HealthState
				serviceState       = d.State
				serviceId          = d.ID
				serviceName        = d.Name
				serviceSystem      = strconv.FormatBool(d.System)
				serviceType        = d.Type

				stackName = o.Name
				stackId   = o.Id
				envName   = o.parent.Name
			)

			// InfinityWorks metrics
			infinityWorksServicesScale.WithLabelValues(serviceName, stackName, serviceSystem).Set(float64(d.Scale))
			for _, y := range healthStates {
				if serviceHealthState == y {
					infinityWorksServicesHealth.WithLabelValues(serviceId, stackId, serviceName, stackName, y, serviceSystem).Set(1)
				} else {
					infinityWorksServicesHealth.WithLabelValues(serviceId, stackId, serviceName, stackName, y, serviceSystem).Set(0)
				}
			}
			for _, y := range serviceStates {
				if serviceState == y {
					infinityWorksServicesState.WithLabelValues(serviceId, stackId, serviceName, stackName, y, serviceSystem).Set(1)
				} else {
					infinityWorksServicesState.WithLabelValues(serviceId, stackId, serviceName, stackName, y, serviceSystem).Set(0)
				}
			}

			// Extended metrics
			extendingServiceHeartbeat.WithLabelValues(envName, stackName, serviceName, serviceSystem, serviceType).Set(float64(1))

			if take, ok := o.Services[serviceName]; ok {
				if take.State != serviceState {
					switch serviceState {
					case "active":
						extendingTotalServiceBootstrap.WithLabelValues(envName, stackName, serviceName, serviceSystem, serviceType).Inc()
						take.BootstrapCount += 1

						if serviceHealthState == "unhealthy" {
							extendingTotalServiceFailure.WithLabelValues(envName, stackName, serviceName, serviceSystem, serviceType).Inc()
							take.FailureCount += 1
						}
					case "error":
						extendingTotalServiceBootstrap.WithLabelValues(envName, stackName, serviceName, serviceSystem, serviceType).Inc()
						take.BootstrapCount += 1

						extendingTotalServiceFailure.WithLabelValues(envName, stackName, serviceName, serviceSystem, serviceType).Inc()
						take.FailureCount += 1
					}
				}

				take.Id = serviceId
				take.Type = serviceType
				take.State = serviceState
				take.System = d.System
			} else {
				bootstrapCount, failureCount := uint64(0), uint64(0)

				switch serviceState {
				case "active":
					extendingTotalServiceBootstrap.WithLabelValues(envName, stackName, serviceName, serviceSystem, serviceType).Inc()
					bootstrapCount = 1

					if serviceHealthState == "unhealthy" {
						extendingTotalServiceFailure.WithLabelValues(envName, stackName, serviceName, serviceSystem, serviceType).Inc()
						failureCount = 1
					} else {
						extendingTotalServiceFailure.WithLabelValues(envName, stackName, serviceName, serviceSystem, serviceType)
					}
				case "error":
					extendingTotalServiceBootstrap.WithLabelValues(envName, stackName, serviceName, serviceSystem, serviceType).Inc()
					bootstrapCount = 1

					extendingTotalServiceFailure.WithLabelValues(envName, stackName, serviceName, serviceSystem, serviceType).Inc()
					failureCount = 1
				default:
					extendingTotalServiceBootstrap.WithLabelValues(envName, stackName, serviceName, serviceSystem, serviceType)
					extendingTotalServiceFailure.WithLabelValues(envName, stackName, serviceName, serviceSystem, serviceType)
				}

				o.Services[serviceName] = service{
					object: &object{
						Id:             serviceId,
						Name:           serviceName,
						State:          serviceState,
						Type:           serviceType,
						BootstrapCount: bootstrapCount,
						FailureCount:   failureCount,
					},
					Instances: make(map[string]instance, 100),
					System:    d.System,
					parent:    o,
				}
			}
		}

		if len(t.Pagination.Next) != 0 {
			url = t.Pagination.Next
		} else {
			break
		}
	}

	wg := &sync.WaitGroup{}
	for _, d := range o.Services {
		wg.Add(1)
		go func(ctx context.Context, svc service) {
			defer wg.Done()

			svc.fetch(ctx, rancherClient)
		}(ctx, d)
	}
	wg.Wait()

	log.Debugln("<< fetch service on stack:", o.Name, "on project:", o.parent.Name)
}

/**
	project class
 */
type project struct {
	*object
	Stacks map[string]stack `json:"stacks"`
}

func (o *project) fetch(ctx context.Context, rancherClient *rancherClient) {
	defer func() {
		if err := recover(); err != nil {
			log.Error(err)
		}
	}()

	if len(o.Id) == 0 {
		return
	}

	log.Debugln("> fetch stacks on project:", o.Name)

	var url string
	if hideSys {
		url = cattleURL + "/projects/" + o.Id + "/stacks?limit=100&sort=id&system=false"
	} else {
		url = cattleURL + "/projects/" + o.Id + "/stacks?limit=100&sort=id"
	}

	for {
		t := rancherClient.get(url)

		for _, d := range t.Data {
			var (
				stackHealthState = d.HealthState
				stackState       = d.State
				stackId          = d.ID
				stackName        = d.Name
				stackSystem      = strconv.FormatBool(d.System)
				stackType        = d.Type

				envName = o.Name
			)

			// InfinityWorks metrics
			for _, y := range healthStates {
				if stackHealthState == y {
					infinityWorksStacksHealth.WithLabelValues(stackId, stackName, y, stackSystem).Set(1)
				} else {
					infinityWorksStacksHealth.WithLabelValues(stackId, stackName, y, stackSystem).Set(0)
				}
			}
			for _, y := range stackStates {
				if stackState == y {
					infinityWorksStacksState.WithLabelValues(stackId, stackName, y, stackSystem).Set(1)
				} else {
					infinityWorksStacksState.WithLabelValues(stackId, stackName, y, stackSystem).Set(0)
				}
			}

			// Extended metrics
			extendingStackHeartbeat.WithLabelValues(envName, stackName, stackSystem, stackType).Set(float64(1))

			if take, ok := o.Stacks[stackName]; ok {
				if take.State != stackState {
					switch stackState {
					case "active":
						extendingTotalStackBootstrap.WithLabelValues(envName, stackName, stackSystem, stackType).Inc()
						take.BootstrapCount += 1

						if stackHealthState == "unhealthy" {
							extendingTotalStackFailure.WithLabelValues(envName, stackName, stackSystem, stackType).Inc()
							take.FailureCount += 1
						}
					case "error":
						extendingTotalStackBootstrap.WithLabelValues(envName, stackName, stackSystem, stackType).Inc()
						take.BootstrapCount += 1

						extendingTotalStackFailure.WithLabelValues(envName, stackName, stackSystem, stackType).Inc()
						take.FailureCount += 1
					}
				}

				take.Id = stackId
				take.Type = stackType
				take.State = stackState
				take.System = d.System
			} else {
				bootstrapCount, failureCount := uint64(0), uint64(0)

				switch stackState {
				case "active":
					extendingTotalStackBootstrap.WithLabelValues(envName, stackName, stackSystem, stackType).Inc()
					bootstrapCount = 1

					if stackHealthState == "unhealthy" {
						extendingTotalStackFailure.WithLabelValues(envName, stackName, stackSystem, stackType).Inc()
						failureCount = 1
					} else {
						extendingTotalStackFailure.WithLabelValues(envName, stackName, stackSystem, stackType)
					}
				case "error":
					extendingTotalStackBootstrap.WithLabelValues(envName, stackName, stackSystem, stackType).Inc()
					bootstrapCount = 1

					extendingTotalStackFailure.WithLabelValues(envName, stackName, stackSystem, stackType).Inc()
					failureCount = 1
				default:
					extendingTotalStackBootstrap.WithLabelValues(envName, stackName, stackSystem, stackType)
					extendingTotalStackFailure.WithLabelValues(envName, stackName, stackSystem, stackType)
				}

				o.Stacks[stackName] = stack{
					object: &object{
						Id:             stackId,
						Name:           stackName,
						State:          stackState,
						Type:           stackType,
						BootstrapCount: bootstrapCount,
						FailureCount:   failureCount,
					},
					Services: make(map[string]service, 100),
					System:   d.System,
					parent:   o,
				}
			}
		}

		if len(t.Pagination.Next) != 0 {
			url = t.Pagination.Next
		} else {
			break
		}
	}

	wg := &sync.WaitGroup{}
	for _, d := range o.Stacks {
		wg.Add(1)
		go func(ctx context.Context, stk stack) {
			defer wg.Done()

			stk.fetch(ctx, rancherClient)
		}(ctx, d)
	}
	wg.Wait()

	log.Debugln("< fetch stacks on project:", o.Name)
}

/**
	metric class
 */
type metric struct {
	m        *sync.RWMutex
	Projects map[string]project `json:"projects"`
}

func (o *metric) recover() {
	defer func() {
		if err := recover(); err != nil {
			log.Error(err)
		}
	}()

	log.Debugln("start recover metrics")

	rancherClient := newRancherClient(0)

	t := rancherClient.get(cattleURL + "/projects")

	for _, d := range t.Data {
		var (
			envId   = d.ID
			envName = d.Name
		)

		if take, ok := o.Projects[envName]; ok {
			take.Id = envId
		} else {
			o.Projects[envName] = project{
				&object{
					Id:   envId,
					Name: envName,
				},
				make(map[string]stack, 100),
			}
		}
	}

	ctx, fn := context.WithTimeout(context.Background(), scrapeTimeoutSeconds)
	defer fn()

	wg := &sync.WaitGroup{}
	for _, d := range o.Projects {
		wg.Add(1)
		go func(ctx context.Context, pro project) {
			defer wg.Done()

			var (
				envId   = pro.Id
				envName = pro.Name
			)

			t := rancherClient.get(cattleURL + "/genericobjects?name=" + genObjName + "&key=" + envId + "&kind=" + genericobjectKind)
			if l := len(t.Data); l != 0 {
				storeProject := t.Data[l-1].ResourceData
				for _, sStack := range storeProject.Stacks {
					var (
						stackId     = sStack.Id
						stackName   = sStack.Name
						stackSystem = strconv.FormatBool(sStack.System)
						stackType   = sStack.Type

						stk = stack{
							object: &object{
								Id:             stackId,
								Name:           stackName,
								State:          sStack.State,
								Type:           stackType,
								BootstrapCount: sStack.BootstrapCount,
								FailureCount:   sStack.FailureCount,
							},
							Services: make(map[string]service, 100),
							System:   sStack.System,
							parent:   &pro,
						}
					)

					pro.Stacks[stackName] = stk

					extendingTotalStackBootstrap.WithLabelValues(envName, stackName, stackSystem, stackType).Add(float64(sStack.BootstrapCount))
					extendingTotalStackFailure.WithLabelValues(envName, stackName, stackSystem, stackType).Add(float64(sStack.FailureCount))

					for _, sService := range sStack.Services {
						var (
							serviceId     = sService.Id
							serviceName   = sService.Name
							serviceSystem = strconv.FormatBool(sService.System)
							serviceType   = sService.Type

							svc = service{
								object: &object{
									Id:             serviceId,
									Name:           serviceName,
									State:          sService.State,
									Type:           serviceType,
									BootstrapCount: sService.BootstrapCount,
									FailureCount:   sService.FailureCount,
								},
								Instances: make(map[string]instance, 100),
								System:    sService.System,
								parent:    &stk,
							}
						)

						stk.Services[serviceName] = svc

						extendingTotalServiceBootstrap.WithLabelValues(envName, stackName, serviceName, serviceSystem, serviceType).Add(float64(sService.BootstrapCount))
						extendingTotalServiceFailure.WithLabelValues(envName, stackName, serviceName, serviceSystem, serviceType).Add(float64(sService.FailureCount))

						for _, sInstance := range sService.Instances {
							var (
								instanceId     = sInstance.Id
								instanceName   = sInstance.Name
								instanceSystem = strconv.FormatBool(sInstance.System)
								instanceType   = sInstance.Type

								ins = instance{
									object: &object{
										Id:             instanceId,
										Name:           instanceName,
										State:          sInstance.State,
										Type:           instanceType,
										BootstrapCount: sInstance.BootstrapCount,
										FailureCount:   sInstance.FailureCount,
									},
									System:      sInstance.System,
									StartupTime: sInstance.StartupTime,
									parent:      &svc,
								}
							)

							svc.Instances[instanceName] = ins

							extendingTotalInstanceBootstrap.WithLabelValues(envName, stackName, serviceName, instanceName, instanceSystem, instanceType).Add(float64(sInstance.BootstrapCount))
							extendingTotalInstanceFailure.WithLabelValues(envName, stackName, serviceName, instanceName, instanceSystem, instanceType).Add(float64(sInstance.FailureCount))

							extendingInstanceBootstrapMsCost.WithLabelValues(envName, stackName, serviceName, instanceName, instanceSystem, instanceType).Set(float64(sInstance.StartupTime))
						}
					}
				}
			}
		}(ctx, d)
	}
	wg.Wait()

	log.Debugln("end recover metrics")
}

func (o *metric) backup() {
	defer func() {
		if err := recover(); err != nil {
			log.Error(err)
		}
	}()

	o.m.RLock()
	defer o.m.RUnlock()
	log.Debugln("start backup metrics")

	genObjIdsMap := make(map[string][]string, len(o.Projects)) // key(projectId):id(genObjId)
	rancherClient := newRancherClient(0)

	// fetch again
	t := rancherClient.get(cattleURL + "/genericobjects?name=" + genObjName + "&kind=" + genericobjectKind)
	for _, d := range t.Data {
		if _, ok := genObjIdsMap[d.Key]; ok {
			genObjIdsMap[d.Key] = append(genObjIdsMap[d.Key], d.ID)
		} else {
			genObjIdsMap[d.Key] = []string{d.ID}
		}
	}

	ctx, fn := context.WithTimeout(context.Background(), backupIntervalSeconds)
	defer fn()

	// create new
	wg := &sync.WaitGroup{}
	for _, d := range o.Projects {
		wg.Add(1)
		go func(ctx context.Context, pro project) {
			defer wg.Done()

			data := make(map[string]interface{})
			data["kind"] = genericobjectKind
			data["name"] = genObjName
			data["key"] = pro.Id
			data["resourceData"] = pro

			dataJson, err := json.Marshal(data)
			if err != nil {
				log.Warnf("error created on %v", err)
				return
			}

			statusCode, err := rancherClient.post(cattleURL+"/genericobjects", bytes.NewBuffer(dataJson))
			if err != nil {
				log.Warnf("error created on %v", err)
			} else if statusCode != http.StatusCreated {
				log.Warnln("error created on ", cattleURL+"/genericobjects")
			} else {
				// delete old
				if genObjIds, ok := genObjIdsMap[pro.Id]; ok {
					for _, genObjId := range genObjIds {
						url := cattleURL + "/genericobjects/" + genObjId + "?action=remove"

						statusCode, err := rancherClient.post(url, nil)
						if err != nil {
							log.Warnf("error deleted on %v", err)
						} else if statusCode != http.StatusAccepted {
							log.Warnln("error deleted on", url)
						}
					}
				}
			}
		}(ctx, d)
	}
	wg.Wait()

	log.Debugln("end backup metrics")
}

func (o *metric) fetch(ctx context.Context) {
	defer func() {
		if err := recover(); err != nil {
			log.Error(err)
		}
	}()

	o.m.Lock()
	defer o.m.Unlock()
	log.Debugln("start fetch metrics")

	// reset InfinityWorks metrics
	infinityWorksHostsState.Reset()
	infinityWorksHostAgentsState.Reset()
	infinityWorksStacksHealth.Reset()
	infinityWorksStacksState.Reset()
	infinityWorksServicesScale.Reset()
	infinityWorksServicesHealth.Reset()
	infinityWorksServicesState.Reset()

	// reset Extending metrics
	extendingInstanceHeartbeat.Reset()
	extendingServiceHeartbeat.Reset()
	extendingStackHeartbeat.Reset()

	rancherClient := newRancherClient(scrapeTimeoutSeconds)
	gwg := &sync.WaitGroup{}

	// InfinityWorks metrics
	gwg.Add(1)
	go func(ctx context.Context) {
		defer gwg.Done()

		t := rancherClient.get(cattleURL + "/hosts")

		for _, d := range t.Data {
			var (
				hostName       = d.HostName
				hostState      = d.State
				hostId         = d.ID
				hostAgentState = d.AgentState
			)

			if len(d.Name) != 0 {
				hostName = d.Name
			}

			for _, y := range hostStates {
				if hostState == y {
					infinityWorksHostsState.WithLabelValues(hostId, hostName, y).Set(1)
				} else {
					infinityWorksHostsState.WithLabelValues(hostId, hostName, y).Set(0)
				}
			}

			for _, y := range agentStates {
				if hostAgentState == y {
					infinityWorksHostAgentsState.WithLabelValues(hostId, hostName, y).Set(1)
				} else {
					infinityWorksHostAgentsState.WithLabelValues(hostId, hostName, y).Set(0)
				}
			}
		}
	}(ctx)

	// Extended metrics
	gwg.Add(1)
	go func(ctx context.Context) {
		defer gwg.Done()

		t := rancherClient.get(cattleURL + "/projects")

		for _, d := range t.Data {
			var (
				envId   = d.ID
				envName = d.Name
			)

			if take, ok := o.Projects[envName]; ok {
				take.Id = envId
			} else {
				o.Projects[envName] = project{
					&object{
						Id:   envId,
						Name: envName,
					},
					make(map[string]stack, 100),
				}
			}
		}

		wg := &sync.WaitGroup{}
		for _, d := range o.Projects {
			wg.Add(1)
			go func(pro project) {
				defer wg.Done()

				pro.fetch(ctx, rancherClient)
			}(d)
		}
		wg.Wait()
	}(ctx)

	gwg.Wait()

	log.Debugln("end fetch metrics")
}

func (o *metric) describe(ch chan<- *prometheus.Desc) {
	/**
		InfinityWorks
	 */
	infinityWorksStacksHealth.Describe(ch)
	infinityWorksStacksState.Describe(ch)
	infinityWorksServicesScale.Describe(ch)
	infinityWorksServicesHealth.Describe(ch)
	infinityWorksServicesState.Describe(ch)
	infinityWorksHostsState.Describe(ch)
	infinityWorksHostAgentsState.Describe(ch)

	/**
		Extended
	 */
	extendingTotalStackBootstrap.Describe(ch)
	extendingTotalStackFailure.Describe(ch)
	extendingTotalServiceBootstrap.Describe(ch)
	extendingTotalServiceFailure.Describe(ch)
	extendingTotalInstanceBootstrap.Describe(ch)
	extendingTotalInstanceFailure.Describe(ch)
	extendingInstanceBootstrapMsCost.Describe(ch)

	extendingInstanceHeartbeat.Describe(ch)
	extendingServiceHeartbeat.Describe(ch)
	extendingStackHeartbeat.Describe(ch)
}

func (o *metric) collect(ch chan<- prometheus.Metric) {
	o.m.RLock()

	/**
		InfinityWorks
	 */
	infinityWorksStacksHealth.Collect(ch)
	infinityWorksStacksState.Collect(ch)
	infinityWorksServicesScale.Collect(ch)
	infinityWorksServicesHealth.Collect(ch)
	infinityWorksServicesState.Collect(ch)
	infinityWorksHostsState.Collect(ch)
	infinityWorksHostAgentsState.Collect(ch)

	/**
		Extended
	 */
	extendingTotalStackBootstrap.Collect(ch)
	extendingTotalStackFailure.Collect(ch)
	extendingTotalServiceBootstrap.Collect(ch)
	extendingTotalServiceFailure.Collect(ch)
	extendingTotalInstanceBootstrap.Collect(ch)
	extendingTotalInstanceFailure.Collect(ch)
	extendingInstanceBootstrapMsCost.Collect(ch)

	extendingInstanceHeartbeat.Collect(ch)
	extendingServiceHeartbeat.Collect(ch)
	extendingStackHeartbeat.Collect(ch)

	o.m.RUnlock()
}
