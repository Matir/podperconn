# PodPerConn

Pod Per Conn accepts an incoming connection and spawns a new pod, then forwards the
connection to the new pod.

This is forwarded as a basic TCP forwarder without regard to the underlying
protocol.

## Copyright

Copyright 2022 Google LLC.  This is not an official Google Product.

## Design

### Initial Setup

1. Load template Deployment.
2. Verify connectivity to k8s cluster.
3. Start listening for connections.

### Per-Connection

1. Accept incoming connection.
2. Attempt spawning a new pod that exposes exactly one port.
3. Start forwarding traffic to port on new pod.
4. On connection close, shut down deployment and close remaining connection.

## Resources

* [Loading Kubernetes YAML
  Files](https://dx13.co.uk/articles/2021/01/15/kubernetes-types-using-go/)
