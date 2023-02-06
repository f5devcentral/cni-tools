package main

import (
	"fmt"
	"io"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	f5_bigip "gitee.com/zongzw/f5-bigip-rest/bigip"
	"gitee.com/zongzw/f5-bigip-rest/utils"
)

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
