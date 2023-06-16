package cnisetup

import "context"

type CNIContext struct {
	CNIConfigs
	context.Context
}

type CNIConfigs []CNIConfig

type BIGIPSelfIP struct {
	Name             string
	IpMask           string `yaml:"ipMask"`
	VlanOrTunnelName string `yaml:"vlanOrTunnelName"`
}

type CNIConfig struct {
	Management struct {
		Username  string
		IpAddress string `yaml:"ipAddress"`
		Port      *int
		password  string
	}
	Flannel *struct {
		Tunnels []struct {
			Name         string
			ProfileName  string `yaml:"profileName"`
			Port         int
			LocalAddress string `yaml:"localAddress"`
			tunnelMac    string
		}
		SelfIPs     []BIGIPSelfIP `yaml:"selfIPs"`
		NodeConfigs []struct {
			PublicIP string `yaml:"publicIP"`
			PodCIDR  string `yaml:"podCIDR"`
		} `yaml:"nodeConfigs"`
	}
	Calico *struct {
		LocalAS  string        `yaml:"localAS"`
		RemoteAS string        `yaml:"remoteAS"`
		SelfIPs  []BIGIPSelfIP `yaml:"selfIPs"`
		PeerIPs  []string      `yaml:"peerIPs"`
	}
	Cilium *struct {
		Tunnels []struct {
			Name         string
			ProfileName  string `yaml:"profileName"`
			Port         int
			LocalAddress string `yaml:"localAddress"`
			tunnelMac    string
		}
		SelfIPs []BIGIPSelfIP `yaml:"selfIPs"`
		Routes  []struct {
			Network     string
			TmInterface string `yaml:"tmInterface"`
		}
	}
	kubeConfig string
}
