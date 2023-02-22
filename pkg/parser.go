package pkg

import (
	"fmt"
	"reflect"
	"strings"

	"gitee.com/zongzw/bigip-kubernetes-gateway/k8s"
	"gitee.com/zongzw/f5-bigip-rest/utils"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

func ParseGatewayRelatedForClass(className string, gwObjs []*gatewayv1beta1.Gateway) (map[string]interface{}, error) {
	defer utils.TimeItToPrometheus()()

	if ActiveSIGs.GetGatewayClass(className) == nil {
		return map[string]interface{}{}, nil
	}

	cgwObjs := []*gatewayv1beta1.Gateway{}
	for _, gw := range gwObjs {
		if gw.Spec.GatewayClassName == gatewayv1beta1.ObjectName(className) {
			cgwObjs = append(cgwObjs, gw)
		}
	}

	rlt := map[string]interface{}{}
	for _, gw := range cgwObjs {
		if cfgs, err := parseGateway(gw); err != nil {
			return map[string]interface{}{}, err
		} else {
			for k, v := range cfgs {
				rlt[k] = v
			}
		}
		hrs := ActiveSIGs.AttachedHTTPRoutes(gw)
		for _, hr := range hrs {
			if cfgs, err := parseHTTPRoute(className, hr); err != nil {
				return map[string]interface{}{}, err
			} else {
				for k, v := range cfgs {
					rlt[k] = v
				}
			}
		}
	}
	return map[string]interface{}{
		"": rlt,
	}, nil
}

// ParseServicesRelatedForAll parse all refered services
func ParseServicesRelatedForAll() (map[string]interface{}, error) {

	// all services that are referenced but may not exist
	svcs := ActiveSIGs.AllAttachedServiceKeys()

	return ParseReferedServiceKeys(svcs)
}

func ParseReferedServiceKeys(svcs []string) (map[string]interface{}, error) {
	rlt := map[string]interface{}{}
	for _, svc := range svcs {

		ns := strings.Split(svc, "/")[0]
		n := strings.Split(svc, "/")[1]

		name := strings.Join([]string{ns, n}, ".")
		rlt["ltm/pool/"+name] = map[string]interface{}{
			"name":    name,
			"monitor": "min 1 of tcp",
			"members": []interface{}{},
		}
		if fmtmbs, err := parseMembersFrom(ns, n); err != nil {
			return rlt, err
		} else {
			rlt["ltm/pool/"+name].(map[string]interface{})["members"] = fmtmbs
		}

		if mon, err := parseMonitorFrom(ns, n); err != nil {
			return rlt, err
		} else {
			rlt["ltm/pool/"+name].(map[string]interface{})["monitor"] = mon
		}

		if err := parseArpsFrom(ns, n, rlt); err != nil {
			return rlt, err
		}
		if err := parseNodesFrom(ns, n, rlt); err != nil {
			return rlt, err
		}
	}

	return map[string]interface{}{
		"": rlt,
	}, nil
}

func parseHTTPRoute(className string, hr *gatewayv1beta1.HTTPRoute) (map[string]interface{}, error) {
	defer utils.TimeItToPrometheus()()

	if hr == nil {
		return map[string]interface{}{}, nil
	}

	rlt := map[string]interface{}{}

	if err := parseiRulesFrom(className, hr, rlt); err != nil {
		return map[string]interface{}{}, err
	}

	return rlt, nil
}

func parseGateway(gw *gatewayv1beta1.Gateway) (map[string]interface{}, error) {
	defer utils.TimeItToPrometheus()()

	if gw == nil {
		return map[string]interface{}{}, nil
	}

	rlt := map[string]interface{}{}
	irules := map[string][]string{}
	listeners := map[string]*gatewayv1beta1.Listener{}

	for i, listener := range gw.Spec.Listeners {
		vsname := gwListenerName(gw, &listener)
		listeners[vsname] = &gw.Spec.Listeners[i]
		if listener.Hostname != nil {
			if _, ok := irules[vsname]; !ok {
				irules[vsname] = []string{}
			}
			irules[vsname] = append(irules[vsname], vsname)
			rule := map[string]interface{}{
				"name": vsname,
				"apiAnonymous": fmt.Sprintf(`
					when HTTP_REQUEST {
						if { not ([HTTP::host] matches "%s") } {
							event HTTP_REQUEST disable
						}
					}
				`, *listener.Hostname),
			}
			rlt["ltm/rule/"+vsname] = rule
		}
	}

	hrs := ActiveSIGs.AttachedHTTPRoutes(gw)
	for _, hr := range hrs {
		for _, pr := range hr.Spec.ParentRefs {
			ns := hr.Namespace
			if pr.Namespace != nil {
				ns = string(*pr.Namespace)
			}
			if pr.SectionName == nil {
				return map[string]interface{}{}, fmt.Errorf("sectionName of paraentRefs is nil, not supported")
			}
			vsname := hrParentName(hr, &pr)
			if _, ok := irules[vsname]; !ok {
				irules[vsname] = []string{}
			}
			routetype := reflect.TypeOf(*hr).Name()
			if routeMatches(ns, listeners[vsname], ActiveSIGs.GetNamespace(hr.Namespace), routetype) {
				irules[vsname] = append(irules[vsname], hrName(hr))
			}
		}
	}
	for _, addr := range gw.Spec.Addresses {
		if *addr.Type == gatewayv1beta1.IPAddressType {
			ipaddr := addr.Value
			for _, listener := range gw.Spec.Listeners {
				var profiles []interface{}
				ipProtocol := ""
				switch listener.Protocol {
				case gatewayv1beta1.HTTPProtocolType:
					profiles = []interface{}{map[string]string{"name": "http"}}
					ipProtocol = "tcp"
				case gatewayv1beta1.TCPProtocolType:
					return map[string]interface{}{}, fmt.Errorf("unsupported ProtocolType: %s", listener.Protocol)
				case gatewayv1beta1.UDPProtocolType:
					return map[string]interface{}{}, fmt.Errorf("unsupported ProtocolType: %s", listener.Protocol)
				case gatewayv1beta1.TLSProtocolType:
					return map[string]interface{}{}, fmt.Errorf("unsupported ProtocolType: %s", listener.Protocol)
				}
				if ipProtocol == "" {
					return map[string]interface{}{}, fmt.Errorf("ipProtocol not set in %s case", listener.Protocol)
				}
				destination := fmt.Sprintf("%s:%d", ipaddr, listener.Port)
				if utils.IsIpv6(ipaddr) {
					destination = fmt.Sprintf("%s.%d", ipaddr, listener.Port)
				}
				name := gwListenerName(gw, &listener)

				rlt["ltm/virtual/"+name] = map[string]interface{}{
					"name":        name,
					"profiles":    profiles,
					"ipProtocol":  ipProtocol,
					"destination": destination,
					"sourceAddressTranslation": map[string]interface{}{
						"type": "automap",
					},
					"rules": []interface{}{},
				}
				if _, ok := irules[name]; ok {
					rlt["ltm/virtual/"+name].(map[string]interface{})["rules"] = irules[name]
				}
			}
		} else {
			return map[string]interface{}{}, fmt.Errorf("unsupported AddressType: %s", *addr.Type)
		}
	}

	return rlt, nil
}

// TODO: find the way to set monitor
func parseMonitorFrom(svcNamespace, svcName string) (string, error) {
	return "min 1 of tcp", nil
}

func parseMembersFrom(svcNamespace, svcName string) ([]interface{}, error) {
	svc := ActiveSIGs.GetService(utils.Keyname(svcNamespace, svcName))
	eps := ActiveSIGs.GetEndpoints(utils.Keyname(svcNamespace, svcName))
	if svc != nil && eps != nil {
		if mbs, err := k8s.FormatMembersFromServiceEndpoints(svc, eps); err != nil {
			return []interface{}{}, err
		} else {
			fmtmbs := []interface{}{}

			for _, mb := range mbs {
				sep := ":"
				if utils.IsIpv6(mb.IpAddr) {
					sep = "."
				}
				fmtmbs = append(fmtmbs, map[string]interface{}{
					"name":    fmt.Sprintf("%s%s%d", mb.IpAddr, sep, mb.TargetPort),
					"address": mb.IpAddr,
				})
			}
			return fmtmbs, nil
		}
	} else {
		return []interface{}{}, nil
	}
}

func parseArpsFrom(svcNamespace, svcName string, rlt map[string]interface{}) error {
	svc := ActiveSIGs.GetService(utils.Keyname(svcNamespace, svcName))
	eps := ActiveSIGs.GetEndpoints(utils.Keyname(svcNamespace, svcName))
	if svc != nil && eps != nil {
		if mbs, err := k8s.FormatMembersFromServiceEndpoints(svc, eps); err != nil {
			return err
		} else {
			prefix := "k8s-"
			for _, mb := range mbs {
				if mb.MacAddr != "" {
					rlt["net/arp/"+prefix+mb.IpAddr] = map[string]interface{}{
						"name":       prefix + mb.IpAddr,
						"ipAddress":  mb.IpAddr,
						"macAddress": mb.MacAddr,
					}
				}
			}
		}
	}
	return nil
}

func parseNodesFrom(svcNamespace, svcName string, rlt map[string]interface{}) error {
	svc := ActiveSIGs.GetService(utils.Keyname(svcNamespace, svcName))
	eps := ActiveSIGs.GetEndpoints(utils.Keyname(svcNamespace, svcName))
	if svc != nil && eps != nil {
		if mbs, err := k8s.FormatMembersFromServiceEndpoints(svc, eps); err != nil {
			return err
		} else {
			for _, mb := range mbs {
				if mb.MacAddr != "" {
					rlt["ltm/node/"+mb.IpAddr] = map[string]interface{}{
						"name":    mb.IpAddr,
						"address": mb.IpAddr,
						"monitor": "default",
						"session": "user-enabled",
					}
				}
			}
		}
	}
	return nil
}

func parseNeighsFrom(routerName, localAs, remoteAs string, addresses []string) (map[string]interface{}, error) {
	rlt := map[string]interface{}{}

	name := strings.Join([]string{"Common", routerName}, ".")
	rlt["net/routing/bgp/"+name] = map[string]interface{}{
		"name":     name,
		"localAs":  localAs,
		"neighbor": []interface{}{},
	}

	fmtneigs := []interface{}{}
	for _, address := range addresses {
		fmtneigs = append(fmtneigs, map[string]interface{}{
			"name":     address,
			"remoteAs": remoteAs,
		})
	}

	rlt["net/routing/bgp/"+name].(map[string]interface{})["neighbor"] = fmtneigs

	return rlt, nil
}

func parseFdbsFrom(tunnelName string, iPToMac map[string]string) (map[string]interface{}, error) {
	rlt := map[string]interface{}{}

	rlt["net/fdb/tunnel/"+tunnelName] = map[string]interface{}{
		"records": []interface{}{},
	}

	fmtrecords := []interface{}{}
	for ip, mac := range iPToMac {
		fmtrecords = append(fmtrecords, map[string]string{
			"name":     mac,
			"endpoint": ip,
		})
	}

	rlt["net/fdb/tunnel/"+tunnelName].(map[string]interface{})["records"] = fmtrecords

	return rlt, nil
}

func ParseNodeConfigs(bc *BIGIPConfig) (map[string]interface{}, error) {
	cfgs := map[string]interface{}{}

	if bc.Calico != nil {
		nIpAddresses := k8s.NodeCache.AllIpAddresses()
		if ccfgs, err := parseNeighsFrom("gwcBGP", bc.Calico.LocalAS, bc.Calico.RemoteAS, nIpAddresses); err != nil {
			return map[string]interface{}{}, err
		} else {
			for k, v := range ccfgs {
				cfgs[k] = v
			}
		}
	}

	if bc.Flannel != nil {
		nIpToMacV4, _ := k8s.NodeCache.AllIpToMac()
		for _, tunnel := range bc.Flannel.Tunnels {
			if fcfgs, err := parseFdbsFrom(tunnel.Name, nIpToMacV4); err != nil {
				return map[string]interface{}{}, err
			} else {
				for k, v := range fcfgs {
					cfgs[k] = v
				}
			}
		}
	}

	return map[string]interface{}{
		"": cfgs,
	}, nil
}
