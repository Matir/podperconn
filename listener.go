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
	"fmt"
	"net"
	"time"
)

type Listener struct {
	port              uint16
	controller        *ContainerController
	listener          net.Listener
	deploymentTimeout time.Duration
	sessionTimeout    time.Duration
	proxyFactory      func(net.Conn) *ContainerProxy
}

func NewListener(controller *ContainerController, port uint16, deploymentTimeout, sessionTimeout time.Duration) *Listener {
	return &Listener{
		port:              port,
		controller:        controller,
		deploymentTimeout: deploymentTimeout,
		sessionTimeout:    sessionTimeout,
		proxyFactory:      NewContainerProxyFactory(controller, deploymentTimeout),
	}
}

func (l *Listener) ListenAndServe() error {
	endpoint := fmt.Sprintf("0.0.0.0:%d", l.port)
	logger.WithField("port", endpoint).Info("Starting listener")
	sock, err := net.Listen("tcp4", endpoint)
	if err != nil {
		return fmt.Errorf("error starting listener: %w", err)
	}
	l.listener = sock
	defer sock.Close()
	for {
		conn, err := sock.Accept()
		if err != nil {
			return fmt.Errorf("error in accept: %w", err)
		}
		go l.HandleConn(conn)
	}
}

func (l *Listener) HandleConn(c net.Conn) {
	clog := logger.WithField("remote", c.RemoteAddr().String())
	clog.Info("incoming connection")
	proxy := l.proxyFactory(c)
	ctx, cancelFunc := context.WithTimeout(context.Background(), l.sessionTimeout)
	defer cancelFunc()
	if err := proxy.Run(ctx); err != nil {
		clog.WithError(err).Info("proxy exited")
	}
}
