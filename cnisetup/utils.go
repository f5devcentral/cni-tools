package cnisetup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	f5_bigip "github.com/zongzw/f5-bigip-rest/bigip"
	"github.com/zongzw/f5-bigip-rest/utils"
)

// func init() {
// 	slog = utils.LogFromContext(context.TODO()).WithLevel(utils.LogLevel_Type_DEBUG)
// }

func getConfigs(CNIConfigs *CNIConfigs, configPath string) error {
	fn := configPath
	f, err := os.Open(fn)
	if err != nil {
		return fmt.Errorf("failed to open file %s for reading: %s", fn, err.Error())
	}
	defer f.Close()
	byaml, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("failed to read file: %s: %s", fn, err)
	}
	if err := yaml.Unmarshal(byaml, &CNIConfigs); err != nil {
		return fmt.Errorf("failed to unmarshal yaml content: %s", err.Error())
	}
	return nil
}

func getCredentials(bigipPassword *string, pwPath string) error {
	fn := pwPath
	if f, err := os.Open(fn); err != nil {
		return err
	} else {
		defer f.Close()
		if b, err := io.ReadAll(f); err != nil {
			return err
		} else {
			*bigipPassword = string(b)
		}
		return nil
	}
}

func enableBGPRouting(bc *f5_bigip.BIGIPContext) error {
	kind := "net/route-domain"
	partition, subfolder, name := "Common", "", "0" // route domain 0

	exists, err := bc.Exist(kind, name, partition, subfolder)
	if err != nil {
		return err
	}
	if exists == nil {
		return fmt.Errorf("route domain 0 must exist. check it")
	}
	// "Cannot mix routing-protocol Legacy and TMOS mode for route-domain (/Common/0)."
	// We need to remove "BGP" from routingProtocol for TMOS mode
	if (*exists)["routingProtocol"] != nil {
		nrps := []interface{}{}
		for _, rp := range (*exists)["routingProtocol"].([]interface{}) {
			if rp.(string) != "BGP" {
				nrps = append(nrps, rp)
			}
		}
		body := map[string]interface{}{
			"routingProtocol": nrps,
		}
		if err := bc.Update(kind, name, partition, subfolder, body); err != nil {
			return err
		}
	}

	return bc.ModifyDbValue("tmrouted.tmos.routing", "enable")
}

func newRestConfig(kubeConfig string) *rest.Config {
	var config *rest.Config
	var err error
	if kubeConfig == "" {
		config, err = rest.InClusterConfig()
		if err != nil {
			panic(err)
		}
	} else {
		config, err = clientcmd.BuildConfigFromFlags("", kubeConfig)
		if nil != err {
			panic(err)
		}
	}
	return config
}

func newKubeClient(kubeConfig string) *kubernetes.Clientset {
	config := newRestConfig(kubeConfig)
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}
	return client
}

func newCalicoClient(kubeConfig string) *dynamic.DynamicClient {
	config := newRestConfig(kubeConfig)
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}
	return client
}

func macAddrOfTunnel(bc *f5_bigip.BIGIPContext, name string) (string, error) {
	slog := utils.LogFromContext(bc.Context)
	// modify sys db tmrouted.tmos.routing value enable
	cmd := fmt.Sprintf(`show net tunnels tunnel %s all-properties`, name)
	slog.Debugf("tmsh cmd: %s", cmd)

	reMac := regexp.MustCompile("(?:[0-9a-fA-F]:?){12}")

	for times, waits := 30, time.Millisecond*100; times > 0; times-- {
		if resp, err := bc.Tmsh(cmd); err != nil {
			return "", err
		} else {
			if rlt, ok := (*resp)["commandResult"]; ok {
				macAddress := reMac.FindString(rlt.(string))
				if macAddress == "" {
					slog.Warnf("not found macAddress from %s", (*resp)["commandResult"].(string))
				} else {
					slog.Debugf("got tunnel %s macAddress: %s", name, macAddress)
					return macAddress, nil
				}
			} else {
				slog.Warnf("empty response from tmsh '%s', no macAddress retrived", cmd)
			}
			<-time.After(waits)
		}
	}

	return "", fmt.Errorf("timeout for getting tunnel mac address")
}

func allNodeIpAddrs(ctx context.Context, ns *v1.NodeList) []string {
	rlt := []string{}
	ipv4, ipv6 := allNodeIPMacAddrs(ctx, ns)
	for k := range ipv4 {
		rlt = append(rlt, k)
	}
	for k := range ipv6 {
		rlt = append(rlt, k)
	}
	return rlt
}

func nodeIsTaint(n *v1.Node) bool {
	for _, taint := range n.Spec.Taints {
		if taint.Key == "node.kubernetes.io/unreachable" && taint.Effect == "NoSchedule" {
			return true
		}
	}
	return false
}

func allNodeIPMacAddrs(ctx context.Context, ns *v1.NodeList) (map[string]string, map[string]string) {
	slog := utils.LogFromContext(ctx)
	rlt4 := map[string]string{}
	rlt6 := map[string]string{}

	for _, n := range ns.Items {
		if nodeIsTaint(&n) {
			continue
		}
		ipaddrv4, ipaddrv6 := "", ""
		macv4, macv6 := "", ""
		// calico
		if _, ok := n.Annotations["projectcalico.org/IPv4Address"]; ok {
			// no mac addr found in 'kubectl get nodes -o yaml'
			ipmask := n.Annotations["projectcalico.org/IPv4Address"]
			ipaddrv4 = strings.Split(ipmask, "/")[0]
		} else {
			// flannel v4
			if _, ok := n.Annotations["flannel.alpha.coreos.com/backend-data"]; ok {
				ipmask := n.Annotations["flannel.alpha.coreos.com/public-ip"]
				ipaddrv4 = strings.Split(ipmask, "/")[0]
				macStr := n.Annotations["flannel.alpha.coreos.com/backend-data"]
				var v map[string]interface{}
				err := json.Unmarshal([]byte(macStr), &v)
				if err != nil {
					slog.Errorf("failed to get mac of %s: %s", n.Name, err.Error())
				}
				macv4 = v["VtepMAC"].(string)
			}
			// flannel v6
			if _, ok := n.Annotations["flannel.alpha.coreos.com/backend-v6-data"]; ok {
				ipaddrv6 = n.Annotations["flannel.alpha.coreos.com/public-ipv6"]
				macStrV6 := n.Annotations["flannel.alpha.coreos.com/backend-v6-data"]
				var v6 map[string]interface{}
				err := json.Unmarshal([]byte(macStrV6), &v6)
				if err != nil {
					slog.Errorf("failed to get mac v6 of %s: %s", n.Name, err.Error())
				}
				macv6 = v6["VtepMAC"].(string)
			}
		}
		if ipaddrv4 != "" {
			rlt4[ipaddrv4] = macv4
		}
		if ipaddrv6 != "" {
			rlt6[ipaddrv6] = macv6
		}

	}
	return rlt4, rlt6
}

func parseNodeConfigs(ctx context.Context, cniconf *CNIConfig, nodeList *v1.NodeList) (map[string]interface{}, error) {
	cfgs := map[string]interface{}{}

	if cniconf.Calico != nil {
		nIpAddresses := allNodeIpAddrs(ctx, nodeList)
		if ccfgs, err := parseNeighsFrom("gwcBGP", cniconf.Calico.LocalAS, cniconf.Calico.RemoteAS, nIpAddresses); err != nil {
			return map[string]interface{}{}, err
		} else {
			for k, v := range ccfgs {
				cfgs[k] = v
			}
		}
	}

	if cniconf.Flannel != nil {
		nIpToMacV4, _ := allNodeIPMacAddrs(ctx, nodeList)
		for _, tunnel := range cniconf.Flannel.Tunnels {
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

// TODO: fix the f5-bigip-rest issue:
//
//	The tunnel (/Common/fl-vxlan) cannot be modified or deleted because it is in use by a VXLAN tunnel (/Common/fl-tunnel).
func parseVxlanProfile(name string, port int) map[string]interface{} {
	return map[string]interface{}{
		"name":         name,
		"floodingType": "none",
		"port":         float64(port), // same type as retrieved from bigip
	}
}

func parseTunnel(name, key, address, profile string) map[string]interface{} {
	return map[string]interface{}{
		"name":         name,
		"key":          key,
		"localAddress": address,
		"profile":      profile,
	}
}

func parseSelf(name, address, vlan string) map[string]interface{} {
	return map[string]interface{}{
		"name":         name,
		"address":      address,
		"vlan":         vlan,
		"allowService": "all",
	}
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
