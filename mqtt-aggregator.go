package main

import (
	"context"
	"log/slog"
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

var logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
	ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
		if a.Key == slog.MessageKey {
			a.Key = "message"
		}
		return a
	},
}))

var conf = config("./config.yaml")
var cm *autopaho.ConnectionManager
var ctx context.Context

func main() {
	slog.SetDefault(logger)
	serverUrl, err := url.Parse(conf.ServerUrl)
	if err != nil {
		logger.Error("Failed to parse server URL", "error", err, "serverUrl", conf.ServerUrl)
		return
	}
	logger.Info("MQTT aggregator starting", "serverUrl", conf.ServerUrl)
	cliCfg := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{serverUrl},
		ConnectUsername:               conf.Username,
		ConnectPassword:               []byte(conf.Password),
		KeepAlive:                     60,
		CleanStartOnInitialConnection: false, // the default
		SessionExpiryInterval:         60,    // Session remains live 60 seconds after disconnect
		OnConnectionUp: func(cm *autopaho.ConnectionManager, connAck *paho.Connack) {
			logger.Info("MQTT connection up")
			if _, err := cm.Subscribe(context.Background(), &paho.Subscribe{
				Subscriptions: conf.inTopics(),
			}); err != nil {
				logger.Error("Failed to subscribe", "error", err)
				return
			}
			logger.Info("MQTT subscription made")
		},
		OnConnectError: func(err error) { logger.Error("Error whilst attempting connection", "error", err) },
		ClientConfig: paho.ClientConfig{
			ClientID: conf.ClientID,
			Session:  state.NewInMemory(),
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){
				func(pr paho.PublishReceived) (bool, error) {
					handle(pr.Packet)
					return true, nil
				}},
			OnClientError: func(err error) { logger.Error("Client error", "error", err) },
			OnServerDisconnect: func(d *paho.Disconnect) {
				if d.Properties != nil {
					logger.Warn("Server requested disconnect", "reason", d.Properties.ReasonString)
				} else {
					logger.Warn("Server requested disconnect", "reasonCode", d.ReasonCode)
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
	logger.Info("Signal caught - exiting")

	// We could cancel the context at this point but will call Disconnect instead (this waits for autopaho to shutdown)
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = cm.Disconnect(ctx)

	logger.Info("Shutdown complete")
}

var topicValues = make(map[string]bool)
var outValues = make(map[string]bool)

func handle(packet *paho.Publish) {
	v, err := strconv.ParseBool(string(packet.Payload))
	if err == nil && topicValues[packet.Topic] != v {
		topicValues[packet.Topic] = v
		runAggregation(packet.Topic)
	} else if err != nil {
		logger.Warn("Failed to parse input payload as bool", "topic", packet.Topic, "payload", string(packet.Payload), "error", err)
	}
}

func runAggregation(topic string) {
	for _, aggregation := range conf.Aggregations {
		if Any(aggregation.InTopics, topic) {
			aggregate(aggregation)
		}
	}
}

type aggregationInput struct {
	Topic string `json:"topic"`
	Value string `json:"value"`
}

type aggregationResult struct {
	Topic string `json:"topic"`
	Value string `json:"value"`
}

func aggregate(aggregation Aggregation) {
	var values = make([]bool, len(aggregation.InTopics))
	input := make([]aggregationInput, len(aggregation.InTopics))
	for i, topic := range aggregation.InTopics {
		v, exists := topicValues[topic]
		if !exists {
			v = false
		}
		values[i] = v
		input[i] = aggregationInput{Topic: topic, Value: strconv.FormatBool(v)}
	}
	switch aggregation.AggregationType {
	case NAND:
		nandAggregate(values, aggregation, input)
	case FORWARD:
		forwardAggregate(values, aggregation, input)
	}
}

func nandAggregate(values []bool, aggregation Aggregation, input []aggregationInput) {
	var res = false
	for _, value := range values {
		if value {
			res = true
			break
		}
	}
	publishBoolResult(res, aggregation, input)
}

func forwardAggregate(values []bool, aggregation Aggregation, input []aggregationInput) {
	for _, value := range values {
		if aggregation.NewValue != nil {
			publishStringResult(*aggregation.NewValue, aggregation, input)
		} else {
			publishBoolResult(value, aggregation, input)
		}
	}
}

func publishBoolResult(res bool, aggregation Aggregation, input []aggregationInput) {
	if outValues[aggregation.OutTopic] != res {
		outValues[aggregation.OutTopic] = res
		var payload string
		if res {
			payload = "1"
		} else {
			payload = "0"
		}
		cm.PublishViaQueue(ctx, &autopaho.QueuePublish{&paho.Publish{
			Topic:   aggregation.OutTopic,
			Payload: []byte(payload),
		}})
		logAggregationResult(aggregation, strconv.FormatBool(res), input)
	}
}

func publishStringResult(value string, aggregation Aggregation, input []aggregationInput) {
	cm.PublishViaQueue(ctx, &autopaho.QueuePublish{&paho.Publish{
		Topic:   aggregation.OutTopic,
		Payload: []byte(value),
	}})
	logAggregationResult(aggregation, value, input)
}

func logAggregationResult(aggregation Aggregation, value string, input []aggregationInput) {
	logger.Info(
		aggregation.OutTopic+" "+string(aggregation.AggregationType)+" result "+value,
		"result", aggregationResult{Topic: aggregation.OutTopic, Value: value},
		"aggregation", aggregation.AggregationType,
		"input", input,
	)
}

func Any(arr []string, s string) bool {
	for _, el := range arr {
		if el == s {
			return true
		}
	}
	return false
}
