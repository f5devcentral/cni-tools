package cnisetup

import (
	"context"

	f5_bigip "github.com/f5devcentral/f5-bigip-rest/bigip"
	"github.com/f5devcentral/f5-bigip-rest/utils"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

type NodeReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	LogLevel   string
	CNIConfigs *CNIConfigs
}

func (r *NodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	lctx := context.WithValue(ctx, utils.CtxKey_Logger, utils.NewLog().WithRequestID(uuid.New().String()).WithLevel(r.LogLevel))
	slog := utils.LogFromContext(lctx)
	slog.Infof("node event: %s", req.Name)

	return ctrl.Result{}, HandleNodeChanges(CNIContext{Context: lctx, CNIConfigs: *r.CNIConfigs})
}

func HandleNodeChanges(cnictx CNIContext) error {
	ocfgs := map[string]interface{}{}
	ncfgs := map[string]interface{}{}

	cniconf := cnictx.CNIConfigs
	ctx := cnictx.Context

	slog := utils.LogFromContext(cnictx.Context)

	for _, c := range cniconf {
		clientset := newKubeClient(c.kubeConfig)
		nodeList, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			slog.Errorf("failed to list nodes: %s", err.Error())
			return err
		}
		if ncfgs, err = parseNodeConfigs(ctx, &c, nodeList); err != nil {
			return err
		}
		bigip := f5_bigip.New(c.bigipUrl(), c.Management.Username, c.Management.password)
		bc := &f5_bigip.BIGIPContext{BIGIP: *bigip, Context: ctx}

		if err := deploy(bc, "Common", &ocfgs, &ncfgs); err != nil {
			slog.Errorf("failed to do deployment: %s", err.Error())
			return err
		}
	}

	return nil
}

func deploy(bc *f5_bigip.BIGIPContext, partition string, ocfgs, ncfgs *map[string]interface{}) error {
	defer utils.TimeItToPrometheus()()

	cmds, err := bc.GenRestRequests(partition, ocfgs, ncfgs)
	if err != nil {
		return err
	}
	return bc.DoRestRequests(cmds)
}
