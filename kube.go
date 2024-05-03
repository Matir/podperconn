// Copyright 2023 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package podpercon

import (
	"context"
	"errors"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/Matir/podperconn/log"
)

const (
	DEFAULT_NAMESPACE = "default"
	UNIQUEID_LABEL    = "uniqid"
	APP_LABEL         = "app"
)

type ContainerController struct {
	endpoint       string
	caPath         string
	certPath       string
	keyPath        string
	deploymentPath string
	namespace      string

	restConfig         *rest.Config
	clientSet          *kubernetes.Clientset
	deploymentTemplate *appsv1.Deployment
}

type ContainerControllerOpt func(*ContainerController)

func NewContainerController(opts ...ContainerControllerOpt) (*ContainerController, error) {
	c := &ContainerController{}
	c.namespace = DEFAULT_NAMESPACE
	for _, o := range opts {
		o(c)
	}

	restConfig, err := makeClusterConfig(c.endpoint, c.caPath, c.certPath, c.keyPath)
	if err != nil {
		return nil, err
	}
	c.restConfig = restConfig

	c.clientSet, err = kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}

	objs, err := LoadObjectsFromFile(c.deploymentPath)
	if err != nil {
		return nil, err
	}
	deps := make([]*appsv1.Deployment, 0, 1)
	for _, o := range objs {
		if dep := o.GetDeployment(); dep != nil {
			deps = append(deps, dep)
		}
	}
	if len(deps) != 1 {
		return nil, fmt.Errorf("Error loading deployments, expected exactly 1, got %d", len(deps))
	}
	c.deploymentTemplate = deps[0]
	// ensure we have an app label
	if _, ok := c.deploymentTemplate.ObjectMeta.Labels[APP_LABEL]; !ok {
		c.deploymentTemplate.ObjectMeta.Labels[APP_LABEL] = c.deploymentTemplate.ObjectMeta.Name
	}
	if _, ok := c.deploymentTemplate.Spec.Template.Labels[APP_LABEL]; !ok {
		c.deploymentTemplate.Spec.Template.Labels[APP_LABEL] = c.deploymentTemplate.ObjectMeta.Name
		c.deploymentTemplate.Spec.Selector.MatchLabels[APP_LABEL] = c.deploymentTemplate.ObjectMeta.Name
	}

	if err := c.CheckServerVersion(); err != nil {
		return nil, fmt.Errorf("error checking server version: %w", err)
	}

	return c, nil
}

func WithEndpoint(endpoint string) func(*ContainerController) {
	return func(c *ContainerController) {
		c.endpoint = endpoint
	}
}

func WithCAPath(caPath string) func(*ContainerController) {
	return func(c *ContainerController) {
		c.caPath = caPath
	}
}

func WithCertPath(certPath string) func(*ContainerController) {
	return func(c *ContainerController) {
		c.certPath = certPath
	}
}

func WithKeyPath(keyPath string) func(*ContainerController) {
	return func(c *ContainerController) {
		c.keyPath = keyPath
	}
}

func WithDeploymentPath(deploymentPath string) func(*ContainerController) {
	return func(c *ContainerController) {
		c.deploymentPath = deploymentPath
	}
}

func WithNamespace(namespace string) func(*ContainerController) {
	return func(c *ContainerController) {
		c.namespace = namespace
	}
}

func makeClusterConfig(endpoint, CAPath, certPath, keyPath string) (*rest.Config, error) {
	if cfg, err := rest.InClusterConfig(); err == nil {
		logger.Info("Using in-cluster config.")
		return cfg, nil
	} else if errors.Is(err, rest.ErrNotInCluster) {
		// handle this path manually
		cfg := &rest.Config{
			Host: endpoint,
			TLSClientConfig: rest.TLSClientConfig{
				CAFile:   CAPath,
				CertFile: certPath,
				KeyFile:  keyPath,
			},
		}
		rest.SetKubernetesDefaults(cfg)
		if err := rest.LoadTLSFiles(cfg); err != nil {
			logger.WithError(err).Error("Error loading TLS Files")
			return nil, err
		}
		logger.Info("Using provided values.")
		return cfg, nil
	} else {
		logger.WithError(err).Error("Error checking in-cluster config.")
		return nil, err
	}
}

func (c *ContainerController) CheckServerVersion() error {
	if c.clientSet == nil {
		return fmt.Errorf("nil clientSet!")
	}
	disc := c.clientSet.Discovery()
	vinfo, err := disc.ServerVersion()
	if err != nil {
		return err
	}
	logger.Infof("Connected to server version: %s", vinfo)
	return nil
}

func (c *ContainerController) MakeDeployment(ctx context.Context, uniqid string) (*appsv1.Deployment, error) {
	thisdep := c.deploymentTemplate.DeepCopy()
	thisdep.ObjectMeta.Name += "-" + uniqid
	thisdep.ObjectMeta.Namespace = c.namespace
	thisdep.ObjectMeta.Labels[UNIQUEID_LABEL] = uniqid
	thisdep.Spec.Template.Labels[UNIQUEID_LABEL] = uniqid
	thisdep.Spec.Selector.MatchLabels[UNIQUEID_LABEL] = uniqid
	// Update spec
	metav1.AddLabelToSelector(thisdep.Spec.Selector, UNIQUEID_LABEL, uniqid)
	thisdep.Spec.Template.ObjectMeta.Labels[UNIQUEID_LABEL] = uniqid
	// We behave badly if replicas > 1
	replicas := int32(1)
	thisdep.Spec.Replicas = &replicas // Requires a pointer

	// Create deployment
	depiface := c.clientSet.AppsV1().Deployments(c.namespace)

	logger.WithField("uniqid", uniqid).WithField("name", thisdep.ObjectMeta.Name).Info("Creating deployment")
	resdep, err := depiface.Create(ctx, thisdep, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}
	return resdep, nil
}

// TODO: delete deployment on failure
func (c *ContainerController) MakeDeploymentAndWaitForReady(ctx context.Context, uniqid string) (*appsv1.Deployment, error) {
	// Setup the watch in advance
	depiface := c.clientSet.AppsV1().Deployments(c.namespace)
	evtlogger := logger.WithField("uniqid", uniqid)

	labelSelector := fmt.Sprintf("%s=%s,%s=%s", APP_LABEL, c.deploymentTemplate.Spec.Template.Labels[APP_LABEL], UNIQUEID_LABEL, uniqid)
	listopts := metav1.ListOptions{
		Watch:         true,
		LabelSelector: labelSelector,
	}
	watcher, err := depiface.Watch(ctx, listopts)
	if err != nil {
		return nil, fmt.Errorf("error starting watch for selector %s: %w", labelSelector, err)
	}
	defer watcher.Stop()

	// we will get the instance later from the watcher
	_, err = c.MakeDeployment(ctx, uniqid)
	if err != nil {
		return nil, err
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	resChan := watcher.ResultChan()

	for {
		select {
		case evt := <-resChan:
			dep, err := c.handleWatchEvent(evt, evtlogger)
			if dep == nil && err == nil {
				continue
			}
			if dep != nil {
				evtlogger.WithField("deployment", fmt.Sprintf("%+v", dep)).Debug("deployment info")
			}
			return dep, err
		case <-ctx.Done():
			evtlogger.WithError(ctx.Err()).Warn("stopped due to context")
			return nil, fmt.Errorf("context stopped waiting for ready: %w", ctx.Err())
		case <-ticker.C:
			evtlogger.Info("waiting for ready")
		}
	}
}

func (c *ContainerController) handleWatchEvent(evt watch.Event, evtlogger log.Logger) (*appsv1.Deployment, error) {
	gvk := evt.Object.GetObjectKind().GroupVersionKind()
	gvkString := gvk.String()
	if gvk.Empty() {
		gvkString = "(empty)"
	}
	evtlogger.WithField("event", string(evt.Type)).WithField("kind", gvkString).Debug("watch event")
	switch evt.Type {
	case watch.Added, watch.Modified:
		// check if we are ready
		dep, ok := evt.Object.(*appsv1.Deployment)
		if !ok {
			evtlogger.WithField("kind", gvkString).Warn("Expected appsv1.Deployment!")
			return nil, fmt.Errorf("expected deployment, but got unexpected object")
		}
		ready, err := isDeploymentReady(dep, evtlogger)
		if err != nil {
			return nil, err
		}
		if ready {
			return dep, nil
		}
		return nil, nil
	case watch.Deleted:
		evtlogger.Warn("Unexpected delete while waiting for ready!")
		return nil, fmt.Errorf("unexpected delete during ready wait")
	case watch.Error:
		// Per https://pkg.go.dev/k8s.io/apimachinery/pkg/watch#Event, this is
		// likely (*api.Status)
		if status, ok := evt.Object.(*metav1.Status); ok {
			evtlogger.WithField("status", fmt.Sprintf("%+v", status)).Error("watch error")
			return nil, fmt.Errorf("watch error: %v: %v", status.Status, status.Message)
		}
		evtlogger.WithField("object", fmt.Sprintf("%+v", evt.Object)).Error("watch unknown error type")
		return nil, fmt.Errorf("watch error of unknown type")
	default:
		evtlogger.WithField("event", string(evt.Type)).Debug("unexpected watch event")
		return nil, nil
	}
}

func isDeploymentReady(dep *appsv1.Deployment, evtlogger log.Logger) (bool, error) {
	if dep == nil {
		return false, fmt.Errorf("Nil deployment in isDeploymentReady")
	}
	evtlogger.WithField(
		"replicas", fmt.Sprintf("%d", dep.Status.Replicas),
	).WithField(
		"readyreplicas", fmt.Sprintf("%d", dep.Status.ReadyReplicas),
	).Debug("Deployment ready state")
	if dep.Status.ReadyReplicas < 1 {
		return false, nil
	}
	res := dep.Status.Replicas == dep.Status.ReadyReplicas
	return res, nil
}

func (c *ContainerController) GetPodsForDeployment(dep *appsv1.Deployment) ([]corev1.Pod, error) {
	selectorString := labels.SelectorFromSet(dep.Spec.Selector.MatchLabels).String()
	podsapi := c.clientSet.CoreV1().Pods(dep.ObjectMeta.Namespace)
	podlist, err := podsapi.List(context.Background(), metav1.ListOptions{
		LabelSelector: selectorString,
	})
	if err != nil {
		logger.WithError(err).WithField("deployment", dep.ObjectMeta.Name).Error("error getting pods for deployment")
		return nil, err
	}
	logger.WithField("deployment", dep.ObjectMeta.Name).Infof("Found %d pods", len(podlist.Items))
	return podlist.Items, nil
}

func (c *ContainerController) DeleteDeployment(dep *appsv1.Deployment) error {
	ctx := context.Background()
	name := dep.ObjectMeta.Name
	logger.WithField("name", name).Info("deleting deployment")
	depiface := c.clientSet.AppsV1().Deployments(dep.ObjectMeta.Namespace)
	return depiface.Delete(ctx, name, metav1.DeleteOptions{})
}
