package main

import (
	"context"
	"f5-tool-setup-cni/cnisetup"
	"flag"
	"os"

	"github.com/zongzw/f5-bigip-rest/utils"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	var bigipConfig, passwordConfig, kubeConfig string
	var daemonMode bool
	var loglevel string
	flag.StringVar(&kubeConfig, "kube-config", "", "Paths to a kubeconfig. Only required if out-of-cluster. i.e. ~/.kube/config")
	flag.StringVar(&bigipConfig, "bigip-config", "./config.yaml", "BIG-IP configuration yaml file.")
	flag.StringVar(&passwordConfig, "bigip-password", "./password", "BIG-IP admin password.")
	flag.BoolVar(&daemonMode, "daemon", false, "run the tool as a daemon to watch k8s node updates")
	flag.StringVar(&loglevel, "log-level", "info", "logging level: debug, info, warn, error, critical")
	flag.Parse()

	slog := utils.LogFromContext(context.TODO()).WithLevel(loglevel)

	var config cnisetup.CNIConfigs
	if err := config.Load(bigipConfig, passwordConfig, kubeConfig); err != nil {
		slog.Errorf(err.Error())
		os.Exit(1)
	}

	cnictx := cnisetup.CNIContext{CNIConfigs: config, Context: context.TODO()}
	slog.Infof(cnictx.Dumps())

	if err := cnictx.Apply(); err != nil {
		slog.Errorf(err.Error())
		os.Exit(1)
	}

	if err := cnisetup.HandleNodeChanges(cnictx); err != nil {
		slog.Errorf("failed to handle nodes: %s", err.Error())
		os.Exit(1)
	}

	if daemonMode {
		restconf := newRestConfig(kubeConfig)
		scheme := runtime.NewScheme()
		utilruntime.Must(clientgoscheme.AddToScheme(scheme))
		mgr, err := ctrl.NewManager(restconf, ctrl.Options{
			Scheme:                 scheme,
			MetricsBindAddress:     "0",
			HealthProbeBindAddress: "0",
		})
		if err != nil {
			slog.Errorf("unable to start manager: %s", err.Error())
			os.Exit(1)
		}

		if err := cnictx.OnTrace(mgr, utils.LogLevel_Type_DEBUG); err != nil {
			slog.Errorf("failed to trace on config: %s", err.Error())
			os.Exit(1)
		}

		if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
			slog.Errorf("failed to start manager: %s", err)
			os.Exit(1)
		}
	}
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
