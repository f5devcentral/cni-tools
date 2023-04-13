package cnisetup

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	f5_bigip "github.com/zongzw/f5-bigip-rest/bigip"
	"github.com/zongzw/f5-bigip-rest/utils"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	confv1 "k8s.io/client-go/applyconfigurations/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

func (cniconfs *CNIConfigs) Load(configPath, passwordPath, kubeConfigPath string) error {
	if err := getConfigs(cniconfs, configPath); err != nil {
		return err
	}

	var password string
	if err := getCredentials(&password, passwordPath); err != nil {
		return err
	}

	for i := range *cniconfs {
		defaultPort := 443
		(*cniconfs)[i].Management.password = password
		if (*cniconfs)[i].Management.Port == nil {
			(*cniconfs)[i].Management.Port = &defaultPort
		}
		(*cniconfs)[i].kubeConfig = kubeConfigPath
	}
	return nil
}

func (cnictx *CNIContext) Dumps() string {
	slog := utils.LogFromContext(cnictx.Context)
	// fmt.Printf("%#v\n", config)
	if bcs, err := json.MarshalIndent(cnictx.CNIConfigs, "", "  "); err != nil {
		slog.Warnf("failed to show the parsed configs: %s", err.Error())
		return ""
	} else {
		return string(bcs)
	}
}

func (cnictx *CNIContext) Apply() error {
	if err := cnictx.applyToBIGIPs(); err != nil {
		return err
	}

	if err := cnictx.applyToK8S(); err != nil {
		return err
	}
	return nil
}

func (cnictx *CNIContext) OnTrace(mgr manager.Manager, loglevel string) error {
	rNode := &NodeReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		LogLevel:   loglevel,
		CNIConfigs: &cnictx.CNIConfigs,
	}
	err := ctrl.NewControllerManagedBy(mgr).For(&v1.Node{}).Complete(rNode)
	if err != nil {
		return err
	}
	return nil
}

func (cnictx *CNIContext) applyToBIGIPs() error {
	errs := []error{}
	ncfgs := map[string]interface{}{}
	for _, c := range cnictx.CNIConfigs {
		bigip := f5_bigip.New(c.bigipUrl(), c.Management.Username, c.Management.password)
		bc := &f5_bigip.BIGIPContext{BIGIP: *bigip, Context: context.TODO()}

		if c.Calico != nil {
			if err := enableBGPRouting(bc); err != nil {
				return err
			}
			calicoCfgs := c.parseCalicoConfig()
			for k, v := range calicoCfgs {
				ncfgs[k] = v
			}
		}

		if c.Flannel != nil {
			flannelCfgs := c.parseFlannelConfig()
			for k, v := range flannelCfgs {
				ncfgs[k] = v
			}
		}
		errs = append(errs, deploy(bc, "Common", nil, &map[string]interface{}{"": ncfgs}))
	}

	err := cnictx.setTunnelMacs()
	return utils.MergeErrors(append(errs, err))
}

func (cnictx *CNIContext) setTunnelMacs() error {
	errs := []error{}
	slog := utils.LogFromContext(cnictx.Context)
	for _, c := range cnictx.CNIConfigs {
		bigip := f5_bigip.New(c.bigipUrl(), c.Management.Username, c.Management.password)
		bc := &f5_bigip.BIGIPContext{BIGIP: *bigip, Context: context.TODO()}
		for i, tunnel := range c.Flannel.Tunnels {
			for times, waits := 30, time.Millisecond*100; times > 0; times-- {
				if mac, err := macAddrOfTunnel(bc, tunnel.Name); err != nil {
					errs = append(errs, err)
					break
				} else if mac == "" {
					// the mac retrieved may be "" just after the tunnel creation.
					<-time.After(waits)
					slog.Debugf("waiting for tunnel creation done.")
				} else {
					c.Flannel.Tunnels[i].tunnelMac = mac
					break
				}
			}
		}
	}
	return utils.MergeErrors(errs)
}

func (cnictx *CNIContext) applyToK8S() error {
	for _, cniconf := range cnictx.CNIConfigs {
		if cniconf.Calico != nil {
			if err := cniconf.setupCalicoOnK8S(cnictx); err != nil {
				return err
			}
		}
		if cniconf.Flannel != nil {
			if err := cniconf.setupFlannelOnK8S(cnictx); err != nil {
				return err
			}
		}
	}
	return nil
}

func (cniconf *CNIConfig) macAddrOf(publicIP string) (string, error) {
	if cniconf.Flannel == nil {
		return "", fmt.Errorf("bigip config flannel is nil")
	}
	for _, tunnel := range cniconf.Flannel.Tunnels {
		if tunnel.LocalAddress == publicIP {
			return tunnel.tunnelMac, nil
		}
	}
	return "", fmt.Errorf("no tunnel with IP address '%s' found in the config", publicIP)
}

func (cniconf *CNIConfig) bigipUrl() string {
	return fmt.Sprintf("https://%s:%d", cniconf.Management.IpAddress, *cniconf.Management.Port)
}

func (cniconf *CNIConfig) setupCalicoOnK8S(ctx context.Context) error {
	slog := utils.LogFromContext(ctx)
	calicoset := newCalicoClient(cniconf.kubeConfig)

	group, version := "crd.projectcalico.org", "v1"
	applyOps := metav1.ApplyOptions{FieldManager: strings.Join([]string{group, version}, "/")}

	gvrBGPConf := schema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: "bgpconfigurations",
	}
	remoteAS, err1 := strconv.ParseInt(cniconf.Calico.RemoteAS, 10, 0)
	localAS, err2 := strconv.ParseInt(cniconf.Calico.LocalAS, 10, 0)
	if err1 != nil || err2 != nil {
		return fmt.Errorf("failed to parse as number from input: %v %v", err1, err2)
	}
	bgpConfName := "default"
	confyaml := unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": strings.Join([]string{group, version}, "/"),
			"kind":       "BGPConfiguration",
			"metadata":   map[string]interface{}{"name": bgpConfName},
			"spec": map[string]interface{}{
				"asNumber":              remoteAS,
				"logSeverityScreen":     "Info",
				"nodeToNodeMeshEnabled": true,
			},
		},
	}
	applyedConf, err := calicoset.Resource(gvrBGPConf).Apply(context.TODO(), bgpConfName, &confyaml, applyOps)
	if err != nil {
		return err
	} else {
		slog.Infof("successfully applied BGPConfiguration: %s", applyedConf.GetName())
	}

	gvrBGPPr := schema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: "bgppeers",
	}

	for _, prIP := range cniconf.Calico.PeerIPs {
		bgpPeerName := fmt.Sprintf("bgppeer-bigip-%s", prIP)
		pryaml := unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": strings.Join([]string{group, version}, "/"),
				"kind":       "BGPPeer",
				"metadata": map[string]interface{}{
					"name": bgpPeerName,
				},

				"spec": map[string]interface{}{
					"asNumber": localAS,
					"peerIP":   prIP,
				},
			},
		}

		appliedPr, err := calicoset.Resource(gvrBGPPr).Apply(context.TODO(), bgpPeerName, &pryaml, applyOps)
		if err != nil {
			return err
		} else {
			slog.Infof("successfully applied BGPPeer: %s", appliedPr.GetName())
		}
	}
	return nil
}

func (cniconf *CNIConfig) setupFlannelOnK8S(ctx context.Context) error {
	slog := utils.LogFromContext(ctx)
	k8sclient := newKubeClient(cniconf.kubeConfig)
	for _, nc := range cniconf.Flannel.NodeConfigs {
		nodeName := fmt.Sprintf("bigip-%s", nc.PublicIP)
		macAddr, err := cniconf.macAddrOf(nc.PublicIP)
		if err != nil {
			return err
		}
		nodeConf := confv1.Node(nodeName)
		nodeConf.WithName(nodeName)
		nodeConf.WithAnnotations(map[string]string{
			"flannel.alpha.coreos.com/public-ip":           nc.PublicIP,
			"flannel.alpha.coreos.com/backend-data":        fmt.Sprintf(`{"VtepMAC":"%s"}`, macAddr),
			"flannel.alpha.coreos.com/backend-type":        "vxlan",
			"flannel.alpha.coreos.com/kube-subnet-manager": "true",
		})
		nodeConf = nodeConf.WithSpec(confv1.NodeSpec().WithPodCIDR(nc.PodCIDR))
		if _, err := k8sclient.CoreV1().Nodes().Apply(context.TODO(), nodeConf, metav1.ApplyOptions{FieldManager: "v1"}); err != nil {
			return err
		} else {
			slog.Infof("node %s created in k8s.", nodeName)
		}
	}
	return nil
}

func (cniconf *CNIConfig) parseFlannelConfig() map[string]interface{} {
	ncfgs := map[string]interface{}{}

	for _, tunnel := range cniconf.Flannel.Tunnels {
		ncfgs["net/tunnels/vxlan/"+tunnel.ProfileName] = parseVxlanProfile(tunnel.ProfileName, tunnel.Port)
		ncfgs["net/tunnels/tunnel/"+tunnel.Name] = parseTunnel(tunnel.Name, "1", tunnel.LocalAddress, tunnel.ProfileName)
	}
	for _, selfip := range cniconf.Flannel.SelfIPs {
		ncfgs["net/self/"+selfip.Name] = parseSelf(selfip.Name, selfip.IpMask, selfip.VlanOrTunnelName)
	}

	return ncfgs
}

func (cniconf *CNIConfig) parseCalicoConfig() map[string]interface{} {

	ncfgs := map[string]interface{}{}
	for _, selfip := range cniconf.Calico.SelfIPs {
		ncfgs["net/self/"+selfip.Name] = parseSelf(selfip.Name, selfip.IpMask, selfip.VlanOrTunnelName)
	}

	return ncfgs
}
