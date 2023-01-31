package main

type BIGIPConfigs []BIGIPConfig

type BIGIPConfig struct {
	Management *struct {
		Username  string
		IpAddress string `yaml:"ipAddress"`
		Port      *int
	}
	Flannel *struct {
		Tunnels []struct {
			Name         string
			ProfileName  string `yaml:"profileName"`
			Port         int
			LocalAddress string `yaml:"localAddress"`
		}
		SelfIPs []struct {
			Name       string
			IpMask     string `yaml:"ipMask"`
			TunnelName string `yaml:"vlanOrTunnelName"`
		} `yaml:"selfIPs"`
	}
	Calico *struct {
		LocalAS  string `yaml:"localAS"`
		RemoteAS string `yaml:"remoteAS"`
	}
}
