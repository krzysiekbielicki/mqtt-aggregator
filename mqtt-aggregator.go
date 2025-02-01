package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	"github.com/eclipse/paho.golang/paho/session/state"
)

var conf = config("./config.yaml")
var cm *autopaho.ConnectionManager
var ctx context.Context

func main() {
	serverUrl, err := url.Parse(conf.ServerUrl)
	if err != nil {
		fmt.Print(err)
		return
	}
	fmt.Println(conf.ServerUrl)
	cliCfg := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{serverUrl},
		ConnectUsername:               conf.Username,
		ConnectPassword:               []byte(conf.Password),
		KeepAlive:                     60,
		CleanStartOnInitialConnection: false, // the default
		SessionExpiryInterval:         60,    // Session remains live 60 seconds after disconnect
		OnConnectionUp: func(cm *autopaho.ConnectionManager, connAck *paho.Connack) {
			fmt.Println("mqtt connection up")
			if _, err := cm.Subscribe(context.Background(), &paho.Subscribe{
				Subscriptions: conf.inTopics(),
			}); err != nil {
				fmt.Printf("failed to subscribe (%s). This is likely to mean no messages will be received.", err)
				return
			}
			fmt.Println("mqtt subscription made")
		},
		OnConnectError: func(err error) { fmt.Printf("error whilst attempting connection: %s\n", err) },
		ClientConfig: paho.ClientConfig{
			ClientID: conf.ClientID,
			Session:  state.NewInMemory(),
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){
				func(pr paho.PublishReceived) (bool, error) {
					handle(pr.Packet)
					return true, nil
				}},
			OnClientError: func(err error) { fmt.Printf("client error: %s\n", err) },
			OnServerDisconnect: func(d *paho.Disconnect) {
				if d.Properties != nil {
					fmt.Printf("server requested disconnect: %s\n", d.Properties.ReasonString)
				} else {
					fmt.Printf("server requested disconnect; reason code: %d\n", d.ReasonCode)
				}
			},
		},
	}

	//
	// Connect to the server
	//
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cm, err = autopaho.NewConnection(ctx, cliCfg)
	if err != nil {
		panic(err)
	}

	// Messages will be handled through the callback so we really just need to wait until a shutdown
	// is requested
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	signal.Notify(sig, syscall.SIGTERM)

	<-sig
	fmt.Println("signal caught - exiting")

	// We could cancel the context at this point but will call Disconnect instead (this waits for autopaho to shutdown)
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = cm.Disconnect(ctx)

	fmt.Println("shutdown complete")
}

var topicValues = make(map[string]bool)
var outValues = make(map[string]bool)

func handle(packet *paho.Publish) {
	v, err := strconv.ParseBool(string(packet.Payload))
	if err == nil && topicValues[packet.Topic] != v {
		topicValues[packet.Topic] = v
		runAggregation(packet.Topic)
	}
}

func runAggregation(topic string) {
	for _, aggregation := range conf.Aggregations {
		if Any(aggregation.InTopics, topic) {
			aggregate(aggregation)
		}
	}
}

func aggregate(aggregation Aggregation) {
	var values = make([]bool, len(aggregation.InTopics))
	fmt.Print("Aggregate items: ")
	for i, topic := range aggregation.InTopics {
		v, exists := topicValues[topic]
		if !exists {
			v = false
		}
		values[i] = v
		fmt.Printf("%s = %t, ", topic, v)
	}
	switch aggregation.AggregationType {
	case NAND:
		nandAggregate(values, aggregation.OutTopic)
	case FORWARD:
		forwardAggregate(values, aggregation.OutTopic)
	}
}

func nandAggregate(values []bool, topic string) {
	var res = false
	for _, value := range values {
		if value {
			res = true
			break
		}
	}
	fmt.Printf("NAND aggregate result %t\n", res)
	publishResult(res, topic)
}

func forwardAggregate(values []bool, topic string) {
	for _, value := range values {
		fmt.Printf("FORWARD aggregate result %t\n", value)
		publishResult(value, topic)
	}
}

func publishResult(res bool, topic string) {
	if outValues[topic] != res {
		outValues[topic] = res
		var payload string
		if res {
			payload = "1"
		} else {
			payload = "0"
		}
		cm.PublishViaQueue(ctx, &autopaho.QueuePublish{&paho.Publish{
			Topic:   topic,
			Payload: []byte(payload),
		}})
		fmt.Printf("Should publish %t onto topic %s \n", res, topic)
	}
}

func Any(arr []string, s string) bool {
	for _, el := range arr {
		if el == s {
			return true
		}
	}
	return false
}
