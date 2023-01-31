package main

import (
	"encoding/json"
	"fmt"
)

func main() {
	var config BIGIPConfigs
	var password string

	if err := getConfigs(&config, "./config.yaml"); err != nil {
		panic(err)
	}

	if err := getCredentials(&password, "./password"); err != nil {
		panic(err)
	}

	for i := range config {
		config[i].Management.password = password
	}

	// fmt.Printf("%#v\n", config)
	if bcs, err := json.MarshalIndent(config, "", "  "); err != nil {
		panic(err)
	} else {
		fmt.Printf("configs: %s\n", bcs)
	}

	if err := setupBIGIPs(&config); err != nil {
		panic(err)
	}
}
