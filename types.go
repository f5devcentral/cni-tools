package main

type BIGIPConfigs []BIGIPConfig

type BIGIPSelfIP struct {
	Name             string
	IpMask           string `yaml:"ipMask"`
	VlanOrTunnelName string `yaml:"vlanOrTunnelName"`
}

type BIGIPConfig struct {
	Management *struct {
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
		}
		SelfIPs []BIGIPSelfIP `yaml:"selfIPs"`
	}
	Calico *struct {
		LocalAS  string        `yaml:"localAS"`
		RemoteAS string        `yaml:"remoteAS"`
		SelfIPs  []BIGIPSelfIP `yaml:"selfIPs"`
	}
}
