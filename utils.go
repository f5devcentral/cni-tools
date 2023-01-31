package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	f5_bigip "gitee.com/zongzw/f5-bigip-rest/bigip"
	"gitee.com/zongzw/f5-bigip-rest/utils"
	v3 "github.com/projectcalico/api/pkg/apis/projectcalico/v3"
	calico_client "github.com/projectcalico/api/pkg/client/clientset_generated/clientset"
	"github.com/projectcalico/api/pkg/lib/numorstring"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	confv1 "k8s.io/client-go/applyconfigurations/core/v1"
)

func getConfigs(bigipConfigs *BIGIPConfigs, configPath string) error {
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
	if err := yaml.Unmarshal(byaml, &bigipConfigs); err != nil {
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

func setupBIGIPs(config *BIGIPConfigs) error {
	if config == nil {
		return nil
	}
	errs := []string{}
	for i, c := range *config {
		if c.Management == nil {
			errs = append(errs, fmt.Sprintf("config #%d: missing management section", i))
			continue
		}

		if c.Management.Port == nil {
			*c.Management.Port = 443
		}
		url := fmt.Sprintf("https://%s:%d", c.Management.IpAddress, *c.Management.Port)
		username := c.Management.Username
		password := c.Management.password
		bigip := f5_bigip.Initialize(url, username, password, "debug")

		bc := &f5_bigip.BIGIPContext{BIGIP: *bigip, Context: context.TODO()}

		if c.Calico != nil {
			if err := EnableBGPRouting(bc); err != nil {
				errs = append(errs, fmt.Sprintf("config #%d: %s", i, err.Error()))
				continue
			}
			for _, selfip := range c.Calico.SelfIPs {
				if err := bc.CreateSelf(selfip.Name, selfip.IpMask, selfip.VlanOrTunnelName); err != nil {
					errs = append(errs, fmt.Sprintf("config #%d: %s", i, err.Error()))
					continue
				}
			}
		}

		if c.Flannel != nil {
			for i, tunnel := range c.Flannel.Tunnels {
				if err := bc.CreateVxlanProfile(tunnel.ProfileName, fmt.Sprintf("%d", tunnel.Port)); err != nil {
					errs = append(errs, fmt.Sprintf("config #%d: %s", i, err.Error()))
					continue
				}
				if err := bc.CreateVxlanTunnel(tunnel.Name, "1", tunnel.LocalAddress, tunnel.ProfileName); err != nil {
					errs = append(errs, fmt.Sprintf("config #%d: %s", i, err.Error()))
					continue
				}
				if mac, err := macAddrOfTunnel(bc, tunnel.Name); err != nil {
					errs = append(errs, fmt.Sprintf("config #%d: %s", i, err.Error()))
					continue
				} else {
					c.Flannel.Tunnels[i].tunnelMac = mac
				}
			}
			for _, selfip := range c.Flannel.SelfIPs {
				if err := bc.CreateSelf(selfip.Name, selfip.IpMask, selfip.VlanOrTunnelName); err != nil {
					errs = append(errs, fmt.Sprintf("config #%d: %s", i, err.Error()))
					continue
				}
			}
		}
	}
	if len(errs) != 0 {
		return fmt.Errorf(strings.Join(errs, "; "))
	} else {
		return nil
	}
}

func EnableBGPRouting(bc *f5_bigip.BIGIPContext) error {
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

func setupK8S(config *BIGIPConfigs) error {
	if config == nil {
		return nil
	}
	for i, bipconf := range *config {
		if bipconf.Calico != nil {
			calicoset := newCalicoClient(kubeConfig)
			bgpConfInf := calicoset.ProjectcalicoV3().BGPConfigurations()
			bgpConfig, err := bgpConfInf.Get(context.TODO(), "default", v1.GetOptions{})
			if err != nil {
				return err
			}
			if bgpConfig == nil {
				var err error
				if bgpConfig, err = bgpConfInf.Create(context.TODO(), v3.NewBGPConfiguration(), v1.CreateOptions{}); err != nil {
					return err
				}
			}

			if as, err := numorstring.ASNumberFromString(bipconf.Calico.RemoteAS); err != nil {
				return err
			} else {
				*bgpConfig.Spec.ASNumber = as
			}

			bgpConfig.Spec.LogSeverityScreen = "Info"
			*bgpConfig.Spec.NodeToNodeMeshEnabled = true

			if _, err := bgpConfInf.Update(context.TODO(), bgpConfig, v1.UpdateOptions{}); err != nil {
				return err
			}

			bgpPrInf := calicoset.ProjectcalicoV3().BGPPeers()
			bgpPeerName := fmt.Sprintf("bgppeer-biggip%d", i+1)
			bgpPeer, err := calicoset.ProjectcalicoV3().BGPPeers().Get(context.TODO(), bgpPeerName, v1.GetOptions{})
			if err != nil {
				return nil
			}

			if bgpPeer == nil {
				var err error
				if bgpPeer, err = bgpPrInf.Create(context.TODO(), v3.NewBGPPeer(), v1.CreateOptions{}); err != nil {
					return err
				}
			}

			bgpPeer.Spec.PeerIP = bipconf.Management.IpAddress
			if as, err := numorstring.ASNumberFromString(bipconf.Calico.LocalAS); err != nil {
				return err
			} else {
				bgpPeer.Spec.ASNumber = as
			}

			if _, err := bgpPrInf.Update(context.TODO(), bgpPeer, v1.UpdateOptions{}); err != nil {
				return err
			}
		}
		if bipconf.Flannel != nil {
			k8sclient := newKubeClient(kubeConfig)
			for _, nc := range bipconf.Flannel.NodeConfigs {
				nodeName := fmt.Sprintf("bigip-%s", nc.PublicIP)
				macAddr, err := bipconf.macAddrOf(nc.PublicIP)
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
				nodeConf.WithSpec(confv1.NodeSpec().WithPodCIDR(nc.PodCIDR))
				if _, err := k8sclient.CoreV1().Nodes().Apply(context.TODO(), nodeConf, v1.ApplyOptions{FieldManager: "v1"}); err != nil {
					return err
				} else {
					slog.Infof("node %s created in k8s.", nodeName)
				}
			}
		}
	}
	return nil
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

func newCalicoClient(kubeConfig string) *calico_client.Clientset {
	config := newRestConfig(kubeConfig)
	client, err := calico_client.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}
	return client
}

func (bipconf *BIGIPConfig) macAddrOf(publicIP string) (string, error) {
	if bipconf.Flannel == nil {
		return "", fmt.Errorf("bigip config flannel is nil")
	}
	for _, tunnel := range bipconf.Flannel.Tunnels {
		if tunnel.LocalAddress == publicIP {
			return tunnel.tunnelMac, nil
		}
	}
	return "", fmt.Errorf("no tunnel with IP address '%s' found in the config", publicIP)
}

func macAddrOfTunnel(bc *f5_bigip.BIGIPContext, name string) (string, error) {
	slog := utils.LogFromContext(bc.Context)
	// modify sys db tmrouted.tmos.routing value enable
	cmd := fmt.Sprintf(`show net tunnels tunnel %s all-properties`, name)
	slog.Debugf("tmsh cmd: %s", cmd)

	if resp, err := bc.Tmsh(cmd); err != nil {
		return "", err
	} else {
		if (*resp)["commandResult"] != nil {
			re := regexp.MustCompile("(?:[0-9a-fA-F]:?){12}")
			macAddress := re.FindString((*resp)["commandResult"].(string))
			if macAddress == "" {
				return "", fmt.Errorf("not found macAddress from %s", (*resp)["commandResult"].(string))
			}
			return macAddress, nil
		} else {
			return "", fmt.Errorf("empty response from tmsh %s, no macaddr retrived", cmd)
		}
	}
}
