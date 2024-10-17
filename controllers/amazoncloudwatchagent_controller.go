// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

// Package controllers contains the main controller, where the reconciliation starts.
package controllers

import (
	"context"
	"fmt"
	"github.com/aws/amazon-cloudwatch-agent-operator/internal/manifests/manifestutils"
	"github.com/aws/amazon-cloudwatch-agent-operator/pkg/featuregate"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	networkingv1 "k8s.io/api/networking/v1"
	policyV1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/aws/amazon-cloudwatch-agent-operator/apis/v1alpha1"
	"github.com/aws/amazon-cloudwatch-agent-operator/internal/config"
	"github.com/aws/amazon-cloudwatch-agent-operator/internal/manifests"
	"github.com/aws/amazon-cloudwatch-agent-operator/internal/manifests/collector"
	collectorStatus "github.com/aws/amazon-cloudwatch-agent-operator/internal/status/collector"
)

// AmazonCloudWatchAgentReconciler reconciles a AmazonCloudWatchAgent object.
type AmazonCloudWatchAgentReconciler struct {
	client.Client
	recorder record.EventRecorder
	scheme   *runtime.Scheme
	log      logr.Logger
	config   config.Config
}

// Params is the set of options to build a new AmazonCloudWatchAgentReconciler.
type Params struct {
	client.Client
	Recorder record.EventRecorder
	Scheme   *runtime.Scheme
	Log      logr.Logger
	Config   config.Config
}

func (r *AmazonCloudWatchAgentReconciler) findCloudWatchAgentOwnedObjects(ctx context.Context, params manifests.Params) (map[types.UID]client.Object, error) {
	ownedObjects := map[types.UID]client.Object{}
	listOps := &client.ListOptions{
		Namespace:     params.OtelCol.Namespace,
		LabelSelector: labels.SelectorFromSet(manifestutils.SelectorLabels(params.OtelCol.ObjectMeta, collector.ComponentAmazonCloudWatchAgent)),
	}
	hpaList := &autoscalingv2.HorizontalPodAutoscalerList{}
	err := r.List(ctx, hpaList, listOps)
	if err != nil {
		return nil, fmt.Errorf("error listing HorizontalPodAutoscalers: %w", err)
	}
	for i := range hpaList.Items {
		ownedObjects[hpaList.Items[i].GetUID()] = &hpaList.Items[i]
	}
	if featuregate.PrometheusOperatorIsAvailable.IsEnabled() {
		servicemonitorList := &monitoringv1.ServiceMonitorList{}
		err = r.List(ctx, servicemonitorList, listOps)
		if err != nil {
			return nil, fmt.Errorf("error listing ServiceMonitors: %w", err)
		}
		for i := range servicemonitorList.Items {
			ownedObjects[servicemonitorList.Items[i].GetUID()] = servicemonitorList.Items[i]
		}
		podMonitorList := &monitoringv1.PodMonitorList{}
		err = r.List(ctx, podMonitorList, listOps)
		if err != nil {
			return nil, fmt.Errorf("error listing PodMonitors: %w", err)
		}
		for i := range podMonitorList.Items {
			ownedObjects[podMonitorList.Items[i].GetUID()] = podMonitorList.Items[i]
		}
	}
	ingressList := &networkingv1.IngressList{}
	err = r.List(ctx, ingressList, listOps)
	if err != nil {
		return nil, fmt.Errorf("error listing Ingresses: %w", err)
	}
	for i := range ingressList.Items {
		ownedObjects[ingressList.Items[i].GetUID()] = &ingressList.Items[i]
	}

	pdbList := &policyV1.PodDisruptionBudgetList{}
	err = r.List(ctx, pdbList, listOps)
	if err != nil {
		return nil, fmt.Errorf("error listing PodDisruptionBudgets: %w", err)
	}
	for i := range pdbList.Items {
		ownedObjects[pdbList.Items[i].GetUID()] = &pdbList.Items[i]
	}
	return ownedObjects, nil
}
func (r *AmazonCloudWatchAgentReconciler) getParams(instance v1alpha1.AmazonCloudWatchAgent) manifests.Params {
	return manifests.Params{
		Config:   r.config,
		Client:   r.Client,
		OtelCol:  instance,
		Log:      r.log,
		Scheme:   r.scheme,
		Recorder: r.recorder,
	}
}

// NewReconciler creates a new reconciler for AmazonCloudWatchAgent objects.
func NewReconciler(p Params) *AmazonCloudWatchAgentReconciler {
	r := &AmazonCloudWatchAgentReconciler{
		Client:   p.Client,
		log:      p.Log,
		scheme:   p.Scheme,
		config:   p.Config,
		recorder: p.Recorder,
	}
	return r
}

// +kubebuilder:rbac:groups="",resources=pods;configmaps;services;serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=apps,resources=daemonsets;deployments;statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;create;update
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors;podmonitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes;routes/custom-host,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cloudwatch.aws.amazon.com,resources=amazoncloudwatchagents,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=cloudwatch.aws.amazon.com,resources=amazoncloudwatchagents/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cloudwatch.aws.amazon.com,resources=amazoncloudwatchagents/finalizers,verbs=get;update;patch

// Reconcile the current state of an OpenTelemetry collector resource with the desired state.
func (r *AmazonCloudWatchAgentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.log.WithValues("amazoncloudwatchagent", req.NamespacedName)

	var instance v1alpha1.AmazonCloudWatchAgent
	if err := r.Get(ctx, req.NamespacedName, &instance); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "unable to fetch AmazonCloudWatchAgent")
		}

		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// We have a deletion, short circuit and let the deletion happen
	if deletionTimestamp := instance.GetDeletionTimestamp(); deletionTimestamp != nil {
		return ctrl.Result{}, nil
	}

	if instance.Spec.ManagementState == v1alpha1.ManagementStateUnmanaged {
		log.Info("Skipping reconciliation for unmanaged AmazonCloudWatchAgent resource", "name", req.String())
		// Stop requeueing for unmanaged AmazonCloudWatchAgent custom resources
		return ctrl.Result{}, nil
	}

	params := r.getParams(instance)

	desiredObjects, buildErr := BuildCollector(params)
	if buildErr != nil {
		return ctrl.Result{}, buildErr
	}

	ownedObjects, err := r.findCloudWatchAgentOwnedObjects(ctx, params)
	if err != nil {
		return ctrl.Result{}, err
	}
	err = reconcileDesiredObjectsWPrune(ctx, r.Client, log, &params.OtelCol, params.Scheme, desiredObjects, ownedObjects)
	return collectorStatus.HandleReconcileStatus(ctx, log, params, err)
}

// SetupWithManager tells the manager what our controller is interested in.
func (r *AmazonCloudWatchAgentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.AmazonCloudWatchAgent{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&corev1.Service{}).
		Owns(&appsv1.Deployment{}).
		Owns(&appsv1.DaemonSet{}).
		Owns(&appsv1.StatefulSet{})

	return builder.Complete(r)
}
