package main

import (
	"fmt"
	"os"

	"github.com/eclipse/paho.golang/paho"
	"gopkg.in/yaml.v3"
)

func config(path string) Config {
	dat, err := os.ReadFile(path)
	check(err)
	var config Config
	fmt.Println(string(dat))
	err = yaml.Unmarshal(dat, &config)
	check(err)
	return config
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

type Config struct {
	ServerUrl    string `yaml:"serverUrl"`
	Username     string `yaml:"username"`
	Password     string `yaml:"password"`
	ClientID     string `yaml:"clientId"`
	Aggregations []Aggregation
}

func (config Config) inTopics() []paho.SubscribeOptions {
	var ret []paho.SubscribeOptions
	for _, agg := range config.Aggregations {
		for _, topic := range agg.InTopics {
			ret = append(ret, paho.SubscribeOptions{Topic: topic})
		}
	}
	return ret
}

type Aggregation struct {
	AggregationType AggregationType `yaml:"type"`
	OutTopic        string          `yaml:"out"`
	InTopics        []string        `yaml:"in"`
}

type AggregationType string

const (
	AND  AggregationType = "AND"
	NAND AggregationType = "NAND"
	OR   AggregationType = "OR"
	NOR  AggregationType = "NOR"
)
