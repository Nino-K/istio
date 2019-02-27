// Copyright 2018 Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package coredatamodel

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/gogo/protobuf/types"

	networking "istio.io/api/networking/v1alpha3"
	"istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pkg/log"
	"istio.io/istio/pkg/mcp/sink"
)

var errUnsupported = errors.New("this operation is not supported by mcp controller")

// CoreDataModel is a combined interface for ConfigStoreCache
// MCP Updater and ServiceDiscovery
type CoreDataModel interface {
	model.ConfigStoreCache
	sink.Updater
}

// Options stores the configurable attributes of a Control
type Options struct {
	IncrementalEDS bool
	DomainSuffix   string
	XDSUpdater     model.XDSUpdater
}

// Controller is a temporary storage for the changes received
// via MCP server
type Controller struct {
	configStoreMu sync.RWMutex
	// keys [type][namespace][name]
	configStore             map[string]map[string]map[string]*model.Config
	descriptorsByCollection map[string]model.ProtoSchema
	options                 *Options
	eventHandlers           map[string][]func(model.Config, model.Event)

	syncedMu sync.Mutex
	synced   map[string]bool
}

// NewController provides a new CoreDataModel controller
func NewController(options *Options) CoreDataModel {
	descriptorsByMessageName := make(map[string]model.ProtoSchema, len(model.IstioConfigTypes))
	synced := make(map[string]bool)
	for _, descriptor := range model.IstioConfigTypes {
		// don't register duplicate descriptors for the same collection
		if _, ok := descriptorsByMessageName[descriptor.Collection]; !ok {
			descriptorsByMessageName[descriptor.Collection] = descriptor
			synced[descriptor.Collection] = false
		}
	}

	return &Controller{
		configStore:             make(map[string]map[string]map[string]*model.Config),
		options:                 options,
		descriptorsByCollection: descriptorsByMessageName,
		eventHandlers:           make(map[string][]func(model.Config, model.Event)),
		synced:                  synced,
	}
}

// ConfigDescriptor returns all the ConfigDescriptors that this
// controller is responsible for
func (c *Controller) ConfigDescriptor() model.ConfigDescriptor {
	return model.IstioConfigTypes
}

// List returns all the config that is stored by type and namespace
// if namespace is empty string it returns config for all the namespaces
func (c *Controller) List(typ, namespace string) (out []model.Config, err error) {
	_, ok := c.ConfigDescriptor().GetByType(typ)
	if !ok {
		return nil, fmt.Errorf("list unknown type %s", typ)
	}
	c.configStoreMu.Lock()
	byType, ok := c.configStore[typ]
	c.configStoreMu.Unlock()
	if !ok {
		return nil, nil
	}

	if namespace == "" {
		// ByType does not need locking since
		// we replace the entire sub-map
		for _, byNamespace := range byType {
			for _, config := range byNamespace {
				out = append(out, *config)
			}
		}
		return out, nil
	}

	for _, config := range byType[namespace] {
		out = append(out, *config)
	}
	return out, nil
}

// Apply receives changes from MCP server and creates the
// corresponding config
func (c *Controller) Apply(change *sink.Change) error {
	descriptor, ok := c.descriptorsByCollection[change.Collection]
	if !ok {
		return fmt.Errorf("apply type not supported %s", change.Collection)
	}

	schema, valid := c.ConfigDescriptor().GetByType(descriptor.Type)
	if !valid {
		return fmt.Errorf("descriptor type not supported %s", change.Collection)
	}

	c.sync(change.Collection)

	// innerStore is [namespace][name]
	innerStore := make(map[string]map[string]*model.Config)
	for _, obj := range change.Objects {
		namespace, name := extractNameNamespace(obj.Metadata.Name)

		createTime := time.Now()
		if obj.Metadata.CreateTime != nil {
			var err error
			if createTime, err = types.TimestampFromProto(obj.Metadata.CreateTime); err != nil {
				return fmt.Errorf("failed to parse %v create_time: %v", obj.Metadata.Name, err)
			}
		}

		conf := &model.Config{
			ConfigMeta: model.ConfigMeta{
				Type:              descriptor.Type,
				Group:             descriptor.Group,
				Version:           descriptor.Version,
				Name:              name,
				Namespace:         namespace,
				ResourceVersion:   obj.Metadata.Version,
				CreationTimestamp: createTime,
				Labels:            obj.Metadata.Labels,
				Annotations:       obj.Metadata.Annotations,
				Domain:            c.options.DomainSuffix,
			},
			Spec: obj.Body,
		}

		if err := schema.Validate(conf.Name, conf.Namespace, conf.Spec); err != nil {
			return err
		}

		namedConfig, ok := innerStore[conf.Namespace]
		if ok {
			namedConfig[conf.Name] = conf
		} else {
			innerStore[conf.Namespace] = map[string]*model.Config{
				conf.Name: conf,
			}
		}
	}

	var prevStore map[string]map[string]*model.Config
	c.configStoreMu.Lock()
	prevStore = c.configStore[descriptor.Type]
	c.configStore[descriptor.Type] = innerStore
	c.configStoreMu.Unlock()

	if c.options.IncrementalEDS &&
		descriptor.Collection == model.ServiceEntry.Collection ||
		descriptor.Collection == model.SyntheticServiceEntry.Collection {
		// only edsUpdate if endpoint changed
		if err := c.incrementalUpdate(innerStore, prevStore); err != nil {
			return err
		}
	} else if descriptor.Type == model.ServiceEntry.Type { // TODO(Nino-k): remove this case once incrementalUpdate is default
		c.serviceEntryEvents(innerStore, prevStore)
		return nil
	} else {
		// notify envoy to request a config push for everything else
		c.options.XDSUpdater.ConfigUpdate(true)
	}

	return nil
}

// HasSynced returns true if the first batch of items has been popped
func (c *Controller) HasSynced() bool {
	var notReady []string

	c.syncedMu.Lock()
	for messageName, synced := range c.synced {
		if !synced {
			notReady = append(notReady, messageName)
		}
	}
	c.syncedMu.Unlock()

	if len(notReady) > 0 {
		log.Infof("Configuration not synced: first push for %v not received", notReady)
		return false
	}
	return true
}

// RegisterEventHandler registers a handler using the type as a key
func (c *Controller) RegisterEventHandler(typ string, handler func(model.Config, model.Event)) {
	c.eventHandlers[typ] = append(c.eventHandlers[typ], handler)
}

// Run is not implemented
func (c *Controller) Run(stop <-chan struct{}) {
	log.Warnf("Run: %s", errUnsupported)
}

// Get is not implemented
func (c *Controller) Get(typ, name, namespace string) *model.Config {
	log.Warnf("get %s", errUnsupported)
	return nil
}

// Update is not implemented
func (c *Controller) Update(config model.Config) (newRevision string, err error) {
	log.Warnf("update %s", errUnsupported)
	return "", errUnsupported
}

// Create is not implemented
func (c *Controller) Create(config model.Config) (revision string, err error) {
	log.Warnf("create %s", errUnsupported)
	return "", errUnsupported
}

// Delete is not implemented
func (c *Controller) Delete(typ, name, namespace string) error {
	return errUnsupported
}

func (c *Controller) sync(collection string) {
	c.syncedMu.Lock()
	c.synced[collection] = true
	c.syncedMu.Unlock()
}

func (c *Controller) incrementalUpdate(innerStore, prevStore map[string]map[string]*model.Config) error {
	for ns, byName := range innerStore {
		for name, config := range byName {
			if prevByNamespace, ok := prevStore[ns]; ok {
				if prevConfig, ok := prevByNamespace[name]; ok {
					update, se := updateEndpoint(prevConfig, config)
					if update && se != nil {
						istioEndpoints := convertEndpoints(se, name, ns)
						hostname := hostName(name, ns, c.options.DomainSuffix)
						if err := c.options.XDSUpdater.EDSUpdate("MCP", hostname, istioEndpoints); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	return nil
}

func updateEndpoint(prevCfg, currentCfg *model.Config) (bool, *networking.ServiceEntry) {
	fmt.Println("1")
	oldSe, ok := prevCfg.Spec.(*networking.ServiceEntry)
	if !ok {
		fmt.Println("2")
		return false, nil
	}
	newSe, ok := currentCfg.Spec.(*networking.ServiceEntry)
	if !ok {
		fmt.Println("3")
		return false, nil
	}
	if len(oldSe.Endpoints) != len(newSe.Endpoints) {
		fmt.Println("4")
		return true, newSe
	}
	if reflect.DeepEqual(oldSe.Endpoints, newSe.Endpoints) {
		fmt.Println("5")
		return false, nil
	}
	// check for equality of Endpoints that changed order
	oldEp, newEp := hash(oldSe.Endpoints, newSe.Endpoints)
	if !equal(oldEp, newEp) {
		fmt.Println("5")
		return true, newSe
	}
	fmt.Println("6")
	return false, nil
}

func hash(oldEp, newEp []*networking.ServiceEntry_Endpoint) (oldEpBuf, newEpBuf []byte) {
	var b bytes.Buffer
	gob.NewEncoder(&b).Encode(oldEp)
	oldEpBuf = b.Bytes()

	b.Reset()
	gob.NewEncoder(&b).Encode(newEp)
	newEpBuf = b.Bytes()
	return oldEpBuf, newEpBuf
}

func equal(a, b []byte) bool {
	a = append(a, b...)
	c := 0
	for _, x := range a {
		// TODO: this could potentially be a wrong
		// doing XOR on a bit by bit basis
		c ^= int(x)
	}
	return c == 0
}

func (c *Controller) serviceEntryEvents(currentStore, prevStore map[string]map[string]*model.Config) {
	dispatch := func(model model.Config, event model.Event) {}
	if handlers, ok := c.eventHandlers[model.ServiceEntry.Type]; ok {
		dispatch = func(model model.Config, event model.Event) {
			log.Debugf("MCP event dispatch: key=%v event=%v", model.Key(), event.String())
			for _, handler := range handlers {
				handler(model, event)
			}
		}
	}

	// add/update
	for namespace, byName := range currentStore {
		for name, config := range byName {
			if prevByNamespace, ok := prevStore[namespace]; ok {
				if prevConfig, ok := prevByNamespace[name]; ok {
					if config.ResourceVersion != prevConfig.ResourceVersion {
						dispatch(*config, model.EventUpdate)
					}
				} else {
					dispatch(*config, model.EventAdd)
				}
			} else {
				dispatch(*config, model.EventAdd)
			}
		}
	}

	// remove
	for namespace, prevByName := range prevStore {
		for name, prevConfig := range prevByName {
			if byNamespace, ok := currentStore[namespace]; ok {
				if _, ok := byNamespace[name]; !ok {
					dispatch(*prevConfig, model.EventDelete)
				}
			} else {
				dispatch(*prevConfig, model.EventDelete)
			}
		}
	}
}

func convertEndpoints(se *networking.ServiceEntry, cfgName, ns string) (endpoints []*model.IstioEndpoint) {
	for _, ep := range se.Endpoints {
		for portName, port := range ep.Ports {
			ep := &model.IstioEndpoint{
				Address:         ep.Address,
				EndpointPort:    port,
				ServicePortName: portName,
				Labels:          ep.Labels,
				UID:             cfgName + "." + ns,
				Network:         ep.Network,
				Locality:        ep.Locality,
				LbWeight:        ep.Weight,
				// ServiceAccount:??
			}
			endpoints = append(endpoints, ep)
		}
	}
	return endpoints
}

func hostName(name, namespace, domainSuffix string) string {
	return name + "." + namespace + ".svc." + domainSuffix
}

func extractNameNamespace(metadataName string) (string, string) {
	segments := strings.Split(metadataName, "/")
	if len(segments) == 2 {
		return segments[0], segments[1]
	}
	return "", segments[0]
}
