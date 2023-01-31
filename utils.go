package main

import (
	"context"
	"encoding/json"
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

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	confv1 "k8s.io/client-go/applyconfigurations/core/v1"
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

func (cniconfs *CNIConfigs) Dumps() string {
	// fmt.Printf("%#v\n", config)
	if bcs, err := json.MarshalIndent(cniconfs, "", "  "); err != nil {
		slog.Warnf("failed to show the parsed configs: %s", err.Error())
		return ""
	} else {
		return string(bcs)
	}
}

func (cniconfs *CNIConfigs) Apply() error {
	if err := cniconfs.applyToBIGIPs(); err != nil {
		return err
	}

	if err := cniconfs.applyToK8S(); err != nil {
		return err
	}
	return nil
}

func (cniconfs *CNIConfigs) applyToBIGIPs() error {
	errs := []string{}
	for i, c := range *cniconfs {
		bigip := f5_bigip.Initialize(c.bigipUrl(), c.Management.Username, c.Management.password, "debug")
		bc := &f5_bigip.BIGIPContext{BIGIP: *bigip, Context: context.TODO()}

		if c.Calico != nil {
			if err := c.setupCalicoOn(bc); err != nil {
				errs = append(errs, fmt.Sprintf("config #%d: %s", i, err.Error()))
				continue
			}
		}

		if c.Flannel != nil {
			if err := c.setupFlannelOn(bc); err != nil {
				errs = append(errs, fmt.Sprintf("config #%d: %s", i, err.Error()))
				continue
			}
		}
	}
	if len(errs) != 0 {
		return fmt.Errorf(strings.Join(errs, "; "))
	} else {
		return nil
	}
}

func (cniconfs *CNIConfigs) applyToK8S() error {
	for _, cniconf := range *cniconfs {
		if cniconf.Calico != nil {
		}
		if cniconf.Flannel != nil {
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

func (cniconf *CNIConfig) setupFlannelOn(bc *f5_bigip.BIGIPContext) error {
	for i, tunnel := range cniconf.Flannel.Tunnels {
		if err := bc.CreateVxlanProfile(tunnel.ProfileName, fmt.Sprintf("%d", tunnel.Port)); err != nil {
			return err
		}
		if err := bc.CreateTunnel(tunnel.Name, "1", tunnel.LocalAddress, tunnel.ProfileName); err != nil {
			return err
		}
		if mac, err := macAddrOfTunnel(bc, tunnel.Name); err != nil {
			return err
		} else {
			cniconf.Flannel.Tunnels[i].tunnelMac = mac
		}
	}
	for _, selfip := range cniconf.Flannel.SelfIPs {
		if err := bc.CreateSelf(selfip.Name, selfip.IpMask, selfip.VlanOrTunnelName); err != nil {
			return err
		}
	}
	return nil
}

func (cniconf *CNIConfig) setupCalicoOn(bc *f5_bigip.BIGIPContext) error {
	if err := EnableBGPRouting(bc); err != nil {
		return err
	}
	for _, selfip := range cniconf.Calico.SelfIPs {
		if err := bc.CreateSelf(selfip.Name, selfip.IpMask, selfip.VlanOrTunnelName); err != nil {
			return err
		}
	}
	return nil
}

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

// func newCalicoClient(kubeConfig string) *calico_client.Clientset {
// 	config := newRestConfig(kubeConfig)
// 	client, err := calico_client.NewForConfig(config)
// 	if err != nil {
// 		panic(err.Error())
// 	}
// 	return client
// }

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
			return "", fmt.Errorf("empty response from tmsh '%s', no macaddr retrived", cmd)
		}
	}
}
