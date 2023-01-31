package main

import (
	"context"
	"encoding/json"
	"flag"

	"gitee.com/zongzw/f5-bigip-rest/utils"
)

func init() {
	// BIGIPs = []*f5_bigip.BIGIP{}
	// kubeConfig = "/Users/zong/.kube/config"
	slog = utils.LogFromContext(context.TODO())
}

func main() {
	var bigipConfig string
	var pwConfig string
	flag.StringVar(&kubeConfig, "kube-config", "", "kube configuration file: i.e. ~/.kube/config")
	flag.StringVar(&bigipConfig, "bigip-config", "./config.yaml", "BIG-IP configuration yaml file.")
	flag.StringVar(&pwConfig, "bigip-password", "./password", "BIG-IP admin password.")
	flag.Parse()

	var config BIGIPConfigs
	var password string
	if err := getConfigs(&config, bigipConfig); err != nil {
		panic(err)
	}
	if err := getCredentials(&password, pwConfig); err != nil {
		panic(err)
	}

	for i := range config {
		config[i].Management.password = password
	}

	// fmt.Printf("%#v\n", config)
	if bcs, err := json.MarshalIndent(config, "", "  "); err != nil {
		panic(err)
	} else {
		slog.Debugf("configs: %s", bcs)
	}

	if err := setupBIGIPs(&config); err != nil {
		panic(err)
	}

	if err := setupK8S(&config); err != nil {
		panic(err)
	}
}
