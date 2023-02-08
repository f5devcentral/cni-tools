package cnisetup

import (
	"context"
	"fmt"

	f5_bigip "gitee.com/zongzw/f5-bigip-rest/bigip"
	"gitee.com/zongzw/f5-bigip-rest/utils"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "k8s.io/api/core/v1"
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
	var nodeList v1.NodeList
	err := r.List(context.TODO(), &nodeList, &client.ListOptions{})
	if err != nil {
		slog.Errorf("failed to list nodes: %s", err.Error())
		return ctrl.Result{}, err
	}
	slog.Infof("node event: %s", req.Name)
	ocfgs := map[string]interface{}{}
	ncfgs := map[string]interface{}{}

	if r.CNIConfigs == nil {
		return ctrl.Result{}, fmt.Errorf("no cni configs provided")
	}
	for _, c := range *r.CNIConfigs {
		if ncfgs, err = ParseNodeConfigs(lctx, &c, &nodeList); err != nil {
			return ctrl.Result{}, err
		}
		bigip := f5_bigip.New(c.bigipUrl(), c.Management.Username, c.Management.password)
		bc := &f5_bigip.BIGIPContext{BIGIP: *bigip, Context: lctx}

		if err := deploy(bc, "Common", &ocfgs, &ncfgs); err != nil {
			slog.Errorf("failed to do deployment: %s", err.Error())
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func deploy(bc *f5_bigip.BIGIPContext, partition string, ocfgs, ncfgs *map[string]interface{}) error {
	defer utils.TimeItToPrometheus()()

	cmds, err := bc.GenRestRequests(partition, ocfgs, ncfgs)
	if err != nil {
		return err
	}
	return bc.DoRestRequests(cmds)
}
