/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	"gitee.com/zongzw/bigip-kubernetes-gateway/pkg"
)

type GatewayReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Adc object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.13.0/pkg/reconcile
func (r *GatewayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	var obj gatewayv1beta1.Gateway
	if err := r.Get(ctx, req.NamespacedName, &obj); err != nil {
		if client.IgnoreNotFound(err) == nil {
			// delete resources
			pobj := pkg.ActiveSIGs.GetGateway(req.NamespacedName.String())
			if ocfgs, err := pkg.ParseGateway(pobj); err != nil {
				return ctrl.Result{}, err
			} else {
				pkg.PendingDeploys <- pkg.DeployRequest{
					From: &ocfgs,
					To:   nil,
					StatusFunc: func() {
						pkg.ActiveSIGs.UnsetGateway(req.NamespacedName.String())
					},
				}
			}

			return ctrl.Result{}, nil
		} else {
			return ctrl.Result{}, err
		}
	} else {
		// upsert resources
		cpObj := obj.DeepCopy()
		if ncfgs, err := pkg.ParseGateway(cpObj); err != nil {
			return ctrl.Result{}, err
		} else {
			oObj := pkg.ActiveSIGs.GetGateway(req.NamespacedName.String())
			if ocfgs, err := pkg.ParseGateway(oObj); err != nil {
				return ctrl.Result{}, err
			} else {
				pkg.PendingDeploys <- pkg.DeployRequest{
					From: &ocfgs,
					To:   &ncfgs,
					StatusFunc: func() {
						obj.Status.Addresses = obj.Spec.Addresses
						if err := r.Status().Update(ctx, &obj); err != nil {
							ctrl.Log.V(1).Error(err, "update error")
						}
						pkg.ActiveSIGs.SetGateway(cpObj)
					},
				}
			}
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GatewayReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gatewayv1beta1.Gateway{}).
		Complete(r)
}
