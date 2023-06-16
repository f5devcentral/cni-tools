# F5 Tool: Setup CNI

This is a tool used to setup CNI integration between BIG-IP and Kubernetes.

Supported CNIs: Flannel, Calico, Cilium.

## Usage

To run it, use the following parameters:

```
  -bigip-config string
        BIG-IP configuration yaml file. (default "./config.yaml")
  -bigip-password string
        BIG-IP admin password. (default "./password")
  -daemon
        run the tool as a daemon to watch k8s node updates
  -kube-config string
        Paths to a kubeconfig. Only required if out-of-cluster. i.e. ~/.kube/config
  -log-level string
        logging level: debug, info, warn, error, critical (default "info")
```

## Functionalities

By providing the above configurations, the tool can automatically do BIG-IP and Kubernetes CNI settings, including:

* Flannel


  * Kubernetes side:

    * Create virtual BIG-IP node for vxlan tunnel setup

  * BIG-IP side:

    * Create vxlan profile for binding to the very tunnel

    * Create vxlan tunnel, and fdb records

    * Create relative self-IP as tunnel VTEP

* Calico:

  * Kubernetes side:

    * Create "crd.projectcalico.org/v1" BGPConfiguration and BGPPeer resources

      Notice that "crd.projectcalico.org/v1" is also known as "projectcalico.org/v3".

  * BIG-IP side:

    * Configure BGP protocol

    * Add kubernetes' nodes as bgp neighbors

* Cilium:

  * Kubernetes side:

    * Do nothing but giving a helm command prompt for user to manually do cilium setup.

      Because cilium installation depends cilium_cli or helm tools, currently, we left this operation to user.

  * BIG-IP side:

    * Create vxlan profile for binding to the very tunnel

    * Create vxlan tunnel, and fdb records

    * Create relative self-IP as tunnel VTEP

    * Create the route for vxlan traffic to/from k8s nodes

* (*In daemon mode only*) Watch kubernetes' node changes and apply the latest states to BIG-IP.

Support IPv6, but not fully verified, please open the issue if necessary.

## Configuration Manual

You may refer to [config.yaml.tmpl](./configs/config.yaml.tmpl) as a sample.

Multiple configurations can be manipulated in a time.

### Configuration Instruction

```yaml
  # BIG-IP management information
- management:
    # username, must be admin
    username: admin
    # management IP address for iControl Rest
    ipAddress: 10.250.2.219
    # optional, management port, default to 443
    port: 443

  # optional, overlay network configuration for flannel CNI mode
  # if it is commented (# flannel level), 
  # there will be no flannel configuration to k8s or bigip
  flannel:
    # tunnels configuration
    tunnels:
        # tunnel name
      - name: fl-tunnel
        # tunnel profile name for binding to the very tunnel
        profileName: fl-vxlan
        # tunnel profile port for binding to the very tunnel
        port: 8472
        # the local address for the tunnel(VTEP)
        # this will be referred in nodeConfigs part.
        localAddress: 10.250.17.219
    # selfips configuration
    selfIPs:
        # the name of the self IP address definition
      - name: flannel-self
        # the IP address associated to the vxlan tunnel
        ipMask: 10.42.20.1/16
        # vlan or tunnel name, should match one of the tunnels
        vlanOrTunnelName: fl-tunnel
      - name: self-17
        ipMask: 10.250.17.219/24
        vlanOrTunnelName: vlan-17
    # configuration for bigip virtual node on k8s side
    nodeConfigs:
        # the public ip for vxlan tunnel connection
        # it will report error if it is not found in tunnels array
      - publicIP: 10.250.17.219
        # the pod CIDR, should match that in selfIPs' 'ipMask'
        # note that, the mask is different
        podCIDR: 10.42.20.0/24
  # optional, underlay network configuration for calico CNI mode
  # if it is commented, 'calico' should also be commented: # calico
  # there will be no calico configuration to k8s or bigip
  calico:
    # AS num on BIG-IP side
    localAS: &as 64512
    # AS num on K8S side, generally, it's same as localAS
    remoteAS: *as
    # self ips for bgp endpoint
    selfIPs:
        # it is as same as that in flannel port.
      - name: self-17
        ipMask: 10.250.17.220/24
        vlanOrTunnelName: vlan-17
    # the self ip used as the peer to interconnect with k8s.
    peerIPs:
      - 10.250.17.220
  # optional, overlay network configuration for cilium CNI mode
  # if it is commented, 'cilium' should also be commented: # cilium
  # there will be no cilium configuration to k8s or bigip
  cilium:
    # tunnels configuration
    tunnels:
        # tunnel name
      - name: fl-tunnel
        # tunnel profile name for binding to the very tunnel
        profileName: fl-vxlan
        # tunnel profile port for binding to the very tunnel
        port: 8472
        # the local address for the tunnel(VTEP)
        # this will be referred in nodeConfigs part.
        localAddress: 10.250.17.219
    # selfips configuration
    selfIPs:
        # the name of the self IP address definition
      - name: flannel-self
        # the IP address associated to the vxlan tunnel.
        #   the mask must NOT be 16 which is as same as the route.
        ipMask: 10.42.20.1/24
        # vlan or tunnel name, should match one of the tunnels
        vlanOrTunnelName: fl-tunnel
      - name: self-17
        ipMask: 10.250.17.219/24
        vlanOrTunnelName: vlan-17
    # route configuration for traffic from/to k8s pods
    routes:
        # the network of pod network cidr.
      - network: 10.0.0.0/16
        # the tunnel name which should exists in tunnels session.
        tmInterface: fl-tunnel
```