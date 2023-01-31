package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	f5_bigip "gitee.com/zongzw/f5-bigip-rest/bigip"
)

func getConfigs(bigipConfigs *BIGIPConfigs, configPath string) error {
	fn := configPath
	f, err := os.Open(fn)
	if err != nil {
		return fmt.Errorf("failed to open file %s for reading: %s", fn, err.Error())
	}
	defer f.Close()
	byaml, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("failed to read file: %s: %s", fn, err)
	}
	if err := yaml.Unmarshal(byaml, &bigipConfigs); err != nil {
		return fmt.Errorf("failed to unmarshal yaml content: %s", err.Error())
	}
	return nil
}

func getCredentials(bigipPassword *string, pwPath string) error {
	fn := pwPath
	if f, err := os.Open(fn); err != nil {
		return err
	} else {
		defer f.Close()
		if b, err := io.ReadAll(f); err != nil {
			return err
		} else {
			*bigipPassword = string(b)
		}
		return nil
	}
}

func setupBIGIPs(config *BIGIPConfigs) error {
	if config == nil {
		return nil
	}
	errs := []string{}
	for i, c := range *config {
		if c.Management == nil {
			errs = append(errs, fmt.Sprintf("config #%d: missing management section", i))
			continue
		}

		if c.Management.Port == nil {
			*c.Management.Port = 443
		}
		url := fmt.Sprintf("https://%s:%d", c.Management.IpAddress, *c.Management.Port)
		username := c.Management.Username
		password := c.Management.password
		bigip := f5_bigip.Initialize(url, username, password, "debug")

		bc := &f5_bigip.BIGIPContext{BIGIP: *bigip, Context: context.TODO()}

		if c.Calico != nil {
			if err := EnableBGPRouting(bc); err != nil {
				errs = append(errs, fmt.Sprintf("config #%d: %s", i, err.Error()))
				continue
			}
			for _, selfip := range c.Calico.SelfIPs {
				if err := bc.CreateSelf(selfip.Name, selfip.IpMask, selfip.VlanOrTunnelName); err != nil {
					errs = append(errs, fmt.Sprintf("config #%d: %s", i, err.Error()))
					continue
				}
			}
		}

		if c.Flannel != nil {
			for i, tunnel := range c.Flannel.Tunnels {
				if err := bc.CreateVxlanProfile(tunnel.ProfileName, fmt.Sprintf("%d", tunnel.Port)); err != nil {
					errs = append(errs, fmt.Sprintf("config #%d: %s", i, err.Error()))
					continue
				}
				if err := bc.CreateVxlanTunnel(tunnel.Name, "1", tunnel.LocalAddress, tunnel.ProfileName); err != nil {
					errs = append(errs, fmt.Sprintf("config #%d: %s", i, err.Error()))
					continue
				}
			}
			for _, selfip := range c.Flannel.SelfIPs {
				if err := bc.CreateSelf(selfip.Name, selfip.IpMask, selfip.VlanOrTunnelName); err != nil {
					errs = append(errs, fmt.Sprintf("config #%d: %s", i, err.Error()))
					continue
				}
			}
		}
	}
	if len(errs) != 0 {
		return fmt.Errorf(strings.Join(errs, "; "))
	} else {
		return nil
	}
}

func EnableBGPRouting(bc *f5_bigip.BIGIPContext) error {
	kind := "net/route-domain"
	partition, subfolder, name := "Common", "", "0" // route domain 0

	exists, err := bc.Exist(kind, name, partition, subfolder)
	if err != nil {
		return err
	}
	if exists == nil {
		return fmt.Errorf("route domain 0 must exist. check it")
	}
	// "Cannot mix routing-protocol Legacy and TMOS mode for route-domain (/Common/0)."
	// We need to remove "BGP" from routingProtocol for TMOS mode
	if (*exists)["routingProtocol"] != nil {
		nrps := []interface{}{}
		for _, rp := range (*exists)["routingProtocol"].([]interface{}) {
			if rp.(string) != "BGP" {
				nrps = append(nrps, rp)
			}
		}
		body := map[string]interface{}{
			"routingProtocol": nrps,
		}
		if err := bc.Update(kind, name, partition, subfolder, body); err != nil {
			return err
		}
	}

	return bc.ModifyDbValue("tmrouted.tmos.routing", "enable")
}
