package main

import (
	"context"
	"flag"
	"os"

	"gitee.com/zongzw/f5-bigip-rest/utils"
)

func init() {
	slog = utils.LogFromContext(context.TODO()).WithLevel(utils.LogLevel_Type_DEBUG)
}

func main() {
	var bigipConfig, passwordConfig, kubeConfig string
	flag.StringVar(&kubeConfig, "kube-config", "", "kube configuration file: i.e. ~/.kube/config")
	flag.StringVar(&bigipConfig, "bigip-config", "./config.yaml", "BIG-IP configuration yaml file.")
	flag.StringVar(&passwordConfig, "bigip-password", "./password", "BIG-IP admin password.")
	flag.Parse()

	var config CNIConfigs
	if err := config.Load(bigipConfig, passwordConfig, kubeConfig); err != nil {
		slog.Errorf(err.Error())
		os.Exit(1)
	} else {
		slog.Debugf(config.Dumps())
	}

	if err := config.Apply(); err != nil {
		slog.Errorf(err.Error())
		os.Exit(1)
	}
}
