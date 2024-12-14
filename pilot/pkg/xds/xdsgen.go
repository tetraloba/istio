// Copyright Istio Authors
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

package xds

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	discovery "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"

	"istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pilot/pkg/networking/util"
	v3 "istio.io/istio/pilot/pkg/xds/v3"
	"istio.io/istio/pkg/env"
	"istio.io/istio/pkg/lazy"
	istioversion "istio.io/istio/pkg/version"
	"istio.io/istio/pkg/xds"
)

// IstioControlPlaneInstance defines the format Istio uses for when creating Envoy config.core.v3.ControlPlane.identifier
type IstioControlPlaneInstance struct {
	// The Istio component type (e.g. "istiod")
	Component string
	// The ID of the component instance
	ID string
	// The Istio version
	Info istioversion.BuildInfo
}

// Evaluate the controlPlane lazily in order to allow "POD_NAME" env var setting after running the process.
var controlPlane = lazy.New(func() (*core.ControlPlane, error) {
	// The Pod Name (instance identity) is in PilotArgs, but not reachable globally nor from DiscoveryServer
	podName := env.Register("POD_NAME", "", "").Get()
	byVersion, err := json.Marshal(IstioControlPlaneInstance{
		Component: "istiod",
		ID:        podName,
		Info:      istioversion.Info,
	})
	if err != nil {
		log.Warnf("XDS: Could not serialize control plane id: %v", err)
	}
	return &core.ControlPlane{Identifier: string(byVersion)}, nil
})

// ControlPlane identifies the instance and Istio version.
func ControlPlane() *core.ControlPlane {
	// Error will never happen because the getter of lazy does not return error.
	cp, _ := controlPlane.Get()
	return cp
}

func (s *DiscoveryServer) findGenerator(typeURL string, con *Connection) model.XdsResourceGenerator {
	if g, f := s.Generators[con.proxy.Metadata.Generator+"/"+typeURL]; f {
		return g
	}
	if g, f := s.Generators[string(con.proxy.Type)+"/"+typeURL]; f {
		return g
	}

	if g, f := s.Generators[typeURL]; f {
		return g
	}

	// XdsResourceGenerator is the default generator for this connection. We want to allow
	// some types to use custom generators - for example EDS.
	g := con.proxy.XdsResourceGenerator
	if g == nil {
		if strings.HasPrefix(typeURL, TypeDebugPrefix) {
			g = s.Generators["event"]
		} else {
			// TODO move this to just directly using the resource TypeUrl
			g = s.Generators["api"] // default to "MCP" generators - any type supported by store
		}
	}
	return g
}

// Push an XDS resource for the given connection. Configuration will be generated
// based on the passed in generator. Based on the updates field, generators may
// choose to send partial or even no response if there are no changes.
func (s *DiscoveryServer) pushXds(con *Connection, w *model.WatchedResource, req *model.PushRequest) error {
	if w == nil {
		return nil
	}
	gen := s.findGenerator(w.TypeUrl, con)
	if gen == nil {
		return nil
	}

	log.Warnf("tetraloba: Hello World! pushXds() has been called for %v", con.proxy.IPAddresses[0])

	t0 := time.Now()

	// If delta is set, client is requesting new resources or removing old ones. We should just generate the
	// new resources it needs, rather than the entire set of known resources.
	// Note: we do not need to account for unsubscribed resources as these are handled by parent removal;
	// See https://www.envoyproxy.io/docs/envoy/latest/api-docs/xds_protocol#deleting-resources.
	// This means if there are only removals, we will not respond.
	var logFiltered string
	if !req.Delta.IsEmpty() && !con.proxy.IsProxylessGrpc() {
		logFiltered = " filtered:" + strconv.Itoa(len(w.ResourceNames)-len(req.Delta.Subscribed))
		w = &model.WatchedResource{
			TypeUrl:       w.TypeUrl,
			ResourceNames: req.Delta.Subscribed.UnsortedList(),
		}
	}
	res, logdata, err := gen.Generate(con.proxy, w, req)
	info := ""
	if len(logdata.AdditionalInfo) > 0 {
		info = " " + logdata.AdditionalInfo
	}
	if len(logFiltered) > 0 {
		info += logFiltered
	}
	if err != nil || res == nil {
		if log.DebugEnabled() {
			log.Debugf("%s: SKIP%s for node:%s%s", v3.GetShortType(w.TypeUrl), req.PushReason(), con.proxy.ID, info)
		}

		return err
	}
	defer func() { recordPushTime(w.TypeUrl, time.Since(t0)) }()

	resp := &discovery.DiscoveryResponse{
		ControlPlane: ControlPlane(),
		TypeUrl:      w.TypeUrl,
		// TODO: send different version for incremental eds
		VersionInfo: req.Push.PushVersion,
		Nonce:       nonce(req.Push.PushVersion),
		Resources:   xds.ResourcesToAny(res),
	}

	configSize := ResourceSize(res)
	configSizeBytes.With(typeTag.Value(w.TypeUrl)).Record(float64(configSize))

	ptype := "PUSH"
	if logdata.Incremental {
		ptype = "PUSH INC"
	}

	if err := xds.Send(con, resp); err != nil {
		if recordSendError(w.TypeUrl, err) {
			log.Warnf("%s: Send failure for node:%s resources:%d size:%s%s: %v",
				v3.GetShortType(w.TypeUrl), con.proxy.ID, len(res), util.ByteCount(configSize), info, err)
		}
		return err
	}

	switch {
	case !req.Full:
		if log.DebugEnabled() {
			log.Debugf("%s: %s%s for node:%s resources:%d size:%s%s",
				v3.GetShortType(w.TypeUrl), ptype, req.PushReason(), con.proxy.ID, len(res), util.ByteCount(configSize), info)
		}
	default:
		debug := ""
		if log.DebugEnabled() {
			// Add additional information to logs when debug mode enabled.
			debug = " nonce:" + resp.Nonce + " version:" + resp.VersionInfo
		}
		log.Infof("%s: %s%s for node:%s resources:%d size:%v%s%s", v3.GetShortType(w.TypeUrl), ptype, req.PushReason(), con.proxy.ID, len(res),
			util.ByteCount(ResourceSize(res)), info, debug)
	}

	return nil
}

func ResourceSize(r model.Resources) int {
	// Approximate size by looking at the Any marshaled size. This avoids high cost
	// proto.Size, at the expense of slightly under counting.
	size := 0
	for _, r := range r {
		size += len(r.Resource.Value)
	}
	return size
}
