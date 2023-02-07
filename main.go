package main

import (
	"context"
	"flag"
	"os"

	"gitee.com/zongzw/f5-bigip-rest/utils"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
)

var (
	scheme = runtime.NewScheme()
	// setupLog = ctrl.Log.WithName("setup")
	// level    = utils.LogLevel_Type_INFO
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
		slog.Infof(config.Dumps())
	}

	if err := config.Apply(); err != nil {
		slog.Errorf(err.Error())
		os.Exit(1)
	}

	// ctrl.SetLogger(slog)
	opts := zap.Options{
		Development: true,
	}
	restconf := newRestConfig(kubeConfig)
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
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

	if err := config.OnTrace(mgr, utils.LogLevel_Type_DEBUG); err != nil {
		slog.Errorf("failed to trace on config: %s", err.Error())
		os.Exit(1)
	}

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		slog.Errorf("failed to start manager: %s", err)
		os.Exit(1)
	}
}
