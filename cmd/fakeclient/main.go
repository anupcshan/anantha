package main

import (
	"crypto/tls"
	"flag"
	"log"
	"os"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

func main() {
	clientID := flag.String("client-id", "hello", "MQTT Client ID")
	publishFile := flag.String("publish-file", "", "File to publish from")
	topic := flag.String("topic", "", "Topic to publish data to")
	flag.Parse()

	cert, err := tls.LoadX509KeyPair("../../tls/client/cert.pem", "../../tls/client/key.pem")
	if err != nil {
		log.Fatal(err)
	}

	mqttClient := mqtt.NewClient(
		mqtt.NewClientOptions().
			AddBroker("mqtts://mqtt.res.carrier.io:443").
			SetClientID(*clientID).
			SetTLSConfig(&tls.Config{
				Certificates: []tls.Certificate{cert},
				NextProtos:   []string{"x-amzn-mqtt-ca"},
			}),
	)

	if token := mqttClient.Connect(); token.Wait() && token.Error() != nil {
		log.Fatalf("Error connecting to MQTT: %s", token.Error())
	}

	log.Printf("Connected")

	blob, err := os.ReadFile(*publishFile)
	if err != nil {
		log.Fatal(err)
	}

	token := mqttClient.Publish(*topic, 0, false, blob)
	token.Wait()
	if err := token.Error(); err != nil {
		log.Fatal(err)
	}
}
