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
	"io"
	"net"
	"os"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/Matir/podperconn/log"
)

// We create one of these per connection
type ContainerProxy struct {
	uniqid            string
	clientConn        net.Conn
	podConn           net.Conn
	deployment        *appsv1.Deployment
	controller        *ContainerController
	deploymentTimeout time.Duration
	logger            log.Logger
}

type DeadlineReader interface {
	io.Reader
	SetReadDeadline(time.Time) error
}

type DeadlineWriter interface {
	io.Writer
	SetWriteDeadline(time.Time) error
}

func NewContainerProxyFactory(controller *ContainerController, deploymentTimeout time.Duration) func(net.Conn) *ContainerProxy {
	return func(client net.Conn) *ContainerProxy {
		p := NewContainerProxy(controller, client)
		p.deploymentTimeout = deploymentTimeout
		return p
	}
}

func NewContainerProxy(controller *ContainerController, client net.Conn) *ContainerProxy {
	uid := RandID()
	return &ContainerProxy{
		uniqid:     uid,
		clientConn: client,
		controller: controller,
		logger:     logger.WithField("uniqid", uid),
	}
}

func (p *ContainerProxy) Run(ctx context.Context) error {
	p.logger.WithField("remote", p.clientConn.RemoteAddr().String()).Info("starting deployment")
	// Start deployment
	dep, err := func() (*appsv1.Deployment, error) {
		deployContext, cancelFunc := context.WithTimeout(ctx, p.deploymentTimeout)
		defer cancelFunc()
		return p.controller.MakeDeploymentAndWaitForReady(deployContext, p.uniqid)
	}()
	if err != nil {
		p.logger.WithError(err).Error("deployment failed")
		return err
	}
	p.deployment = dep
	p.logger.WithField("deployment", dep.ObjectMeta.Name).Info("deployment created")
	// defer destruction of deployment
	defer p.shutdown()

	// Connect to pod
	hostport, err := p.getPodHostPort(dep)
	if err != nil {
		return err
	}
	conn, err := func() (net.Conn, error) {
		dialCtx, cancelFunc := context.WithTimeout(ctx, p.deploymentTimeout)
		defer cancelFunc()
		return p.retryingDialer(dialCtx, hostport)
	}()
	if err != nil {
		return err
	}
	p.podConn = conn

	// Perform copies & close
	p.logger.Info("starting proxy")
	defer p.logger.Info("proxying ended")
	return p.proxyData(ctx)
}

func (p *ContainerProxy) copyWithContextAndTimeout(ctx context.Context, dst DeadlineWriter, src DeadlineReader, opTimeout time.Duration, cancelFunc func()) {
	buf := make([]byte, 4096)
	for {
		src.SetReadDeadline(time.Now().Add(opTimeout))
		n, err := src.Read(buf)
		if err != nil && !errors.Is(err, os.ErrDeadlineExceeded) {
			p.logger.Info("read failed: %s", err)
			break
		}
		if n > 0 {
			dst.SetWriteDeadline(time.Now().Add(opTimeout))
			_, err := dst.Write(buf[:n])
			if err != nil {
				p.logger.Info("write failed: %s", err)
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
	}
	cancelFunc()
}

func (p *ContainerProxy) proxyData(ctx context.Context) error {
	// connections established, proxy here
	if p.clientConn == nil {
		return fmt.Errorf("client conn nil")
	}
	defer p.clientConn.Close()
	if p.podConn == nil {
		return fmt.Errorf("pod conn nil")
	}
	defer p.podConn.Close()

	copyCtx, cancelFunc := context.WithCancel(ctx)
	opTimeout := 5 * time.Second

	go p.copyWithContextAndTimeout(copyCtx, p.podConn, p.clientConn, opTimeout, cancelFunc)
	go p.copyWithContextAndTimeout(copyCtx, p.clientConn, p.podConn, opTimeout, cancelFunc)

	// block until done
	<-ctx.Done()

	// TODO: return a sensical error?
	return nil
}

func (p *ContainerProxy) getPodHostPort(dep *appsv1.Deployment) (string, error) {
	podlist, err := p.controller.GetPodsForDeployment(dep)
	if err != nil {
		return "", err
	}
	switch len(podlist) {
	case 0:
		return "", fmt.Errorf("found no pods for deployment")
	case 1:
		break
	default:
		return "", fmt.Errorf("got more than one pod for deployment: %d pods", len(podlist))
	}
	pod := podlist[0]
	podName := pod.ObjectMeta.Name
	podlog := p.logger.WithField("pod", podName)
	podIP := pod.Status.PodIP
	podPort, err := GetFirstPortFromPod(&pod, podlog)
	if err != nil {
		return "", err
	}
	hostPort := net.JoinHostPort(podIP, fmt.Sprintf("%d", podPort))
	podlog.WithField("hostport", hostPort).Info("found hostport for pod")
	return hostPort, nil
}

func GetFirstPortFromPod(p *corev1.Pod, podLog log.Logger) (uint16, error) {
	ctrs := p.Spec.Containers
	if len(ctrs) < 1 {
		return 0, fmt.Errorf("Pod with no containers?!")
	}
	if len(ctrs) > 1 {
		podLog.Warnf("pod has %d containers", len(ctrs))
	}
	ctr := ctrs[0]
	if len(ctr.Ports) < 1 {
		return 0, fmt.Errorf("No port on container %s!", ctr.Name)
	}
	if len(ctr.Ports) > 1 {
		podLog.Warnf("container %s has %d ports", ctr.Name, len(ctr.Ports))
	}
	return uint16(ctr.Ports[0].ContainerPort), nil
}

// retries with a delay until context expires
func (p *ContainerProxy) retryingDialer(ctx context.Context, hostport string) (net.Conn, error) {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
	}
	diallog := p.logger.WithField("hostport", hostport)
	for {
		conn, err := dialer.DialContext(ctx, "tcp", hostport)
		if err != nil {
			diallog.WithError(err).Warn("dial failed")
			// did the context end?  if so, we return this error
			if ctx.Err() != nil {
				diallog.WithError(ctx.Err()).Error("dial failed; context expired")
				return nil, err
			}
			continue // try again
		}
		diallog.Info("connected")
		return conn, nil
	}
}

func (p *ContainerProxy) shutdown() {
	// shutdown the container
	if err := p.controller.DeleteDeployment(p.deployment); err != nil {
		p.logger.WithError(err).Warn("deployment delete failed")
	}
}
