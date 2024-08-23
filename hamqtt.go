package main

import (
	"fmt"
	"log"

	carrier "github.com/anupcshan/anantha/pb"
	mqtt_paho "github.com/eclipse/paho.mqtt.golang"
)

type HAMQTT struct {
	addr         string
	topicPrefix  string
	loadedValues *LoadedValues

	sendCommand func([]*carrier.ConfigSetting)
	mqttClient  mqtt_paho.Client
}

func NewHAMQTT(addr string, topicPrefix string, loadedValues *LoadedValues, sendCommand func([]*carrier.ConfigSetting)) *HAMQTT {
	return &HAMQTT{
		addr:         addr,
		topicPrefix:  topicPrefix,
		loadedValues: loadedValues,
		sendCommand:  sendCommand,
	}
}

func invertMap[K, V comparable](m map[K]V) map[V]K {
	i := make(map[V]K)
	for k, v := range m {
		i[v] = k
	}

	return i
}

var (
	// Translate Carrier mode to Home Assistant HVAC mode
	carrierModeToHAMode = map[string]string{
		"auto":    "auto",
		"off":     "off",
		"cool":    "cool",
		"heat":    "heat",
		"fanonly": "fan_only",
	}

	HAModeToCarrierMode = invertMap(carrierModeToHAMode)

	// Translate Carrier fan mode to Home Assistant fan mode
	carrierFanModeToHA = map[string]string{
		"off":  "auto",
		"low":  "low",
		"med":  "medium",
		"high": "high",
	}

	HAFanModeToCarrier = invertMap(carrierFanModeToHA)
)

// Need to return one of:
// off, heating, cooling, drying, idle, fan
func computeCurrentAction(opmode string, opstat string) string {
	switch opmode {
	case "heating", "off":
		return opmode
	case "cooling":
		if opstat != "dehumidify" {
			return opmode
		} else {
			return "drying"
		}
	}

	return "off"
}

func (h *HAMQTT) publish(topicSuffix string, value string) error {
	token := h.mqttClient.Publish(
		fmt.Sprintf("%s/%s", h.topicPrefix, topicSuffix),
		0, true,
		value,
	)

	token.Wait()
	return token.Error()
}

func (h *HAMQTT) subscribe(topicSuffix string, handler func(_ mqtt_paho.Client, msg mqtt_paho.Message)) {
	token := h.mqttClient.Subscribe(
		fmt.Sprintf("%s/%s", h.topicPrefix, topicSuffix),
		0,
		handler,
	)

	token.Wait()
}

func (h *HAMQTT) Run() {
	if h.addr == "" && h.topicPrefix == "" {
		log.Printf("Not initialiazing HA MQTT with addr=%s topicPrefix=%s", h.addr, h.topicPrefix)
		return
	}

	h.mqttClient = mqtt_paho.NewClient(
		mqtt_paho.NewClientOptions().AddBroker(h.addr),
	)

	log.Printf("Connecting to %s", h.addr)
	if token := h.mqttClient.Connect(); token.Wait() && token.Error() != nil {
		log.Fatalf("Error connecting to MQTT: %s", token.Error())
	}
	log.Printf("Connected to %s", h.addr)

	h.subscribe("mode/set",
		func(_ mqtt_paho.Client, msg mqtt_paho.Message) {
			log.Printf("About to set mode to %s", msg.Payload())
			h.sendCommand([]*carrier.ConfigSetting{
				{
					Name:       "system/mode",
					ConfigType: carrier.ConfigType_CT_STRING,
					Value: &carrier.ConfigSetting_MaybeStrValue{
						MaybeStrValue: []byte(HAModeToCarrierMode[string(msg.Payload())]),
					},
				},
			})
		},
	)

	h.subscribe("zone/1/fanmode/set",
		func(_ mqtt_paho.Client, msg mqtt_paho.Message) {
			activityVal := h.loadedValues.Get("1/currentActivity")
			if activityVal.value == nil {
				return
			}
			currentActivity := string(activityVal.value.GetMaybeStrValue())
			log.Printf("About to set fan mode for %s to %s", currentActivity, msg.Payload())
			h.sendCommand([]*carrier.ConfigSetting{
				{
					Name:       fmt.Sprintf("zones/1/activities/%s/fan", currentActivity),
					ConfigType: carrier.ConfigType_CT_STRING,
					Value: &carrier.ConfigSetting_MaybeStrValue{
						MaybeStrValue: []byte(HAFanModeToCarrier[string(msg.Payload())]),
					},
				},
			})
		},
	)

	h.subscribe("zone/1/preset_mode/set",
		func(_ mqtt_paho.Client, msg mqtt_paho.Message) {
			switch string(msg.Payload()) {
			case "none":
				// Reset to schedule
				log.Println("About to reset preset mode for zone 1")
				h.sendCommand([]*carrier.ConfigSetting{
					{
						Name:       "zones/1/hold/hold",
						ConfigType: carrier.ConfigType_CT_BOOL,
						Value: &carrier.ConfigSetting_BoolValue{
							BoolValue: false,
						},
					},
					{
						Name:       "zones/1/hold/holdActivity",
						ConfigType: carrier.ConfigType_CT_STRING,
						Value:      &carrier.ConfigSetting_MaybeStrValue{},
					},
					{
						Name:       "zones/1/hold/otmr",
						ConfigType: carrier.ConfigType_CT_STRING,
						Value:      &carrier.ConfigSetting_MaybeStrValue{},
					},
				})
			default:
				log.Printf("About to set preset mode for zone 1 to %s", msg.Payload())
				h.sendCommand([]*carrier.ConfigSetting{
					{
						Name:       "zones/1/hold/hold",
						ConfigType: carrier.ConfigType_CT_BOOL,
						Value: &carrier.ConfigSetting_BoolValue{
							BoolValue: true,
						},
					},
					{
						Name:       "zones/1/hold/holdActivity",
						ConfigType: carrier.ConfigType_CT_STRING,
						Value: &carrier.ConfigSetting_MaybeStrValue{
							MaybeStrValue: msg.Payload(),
						},
					},
					{
						Name:       "zones/1/hold/otmr",
						ConfigType: carrier.ConfigType_CT_STRING,
						Value:      &carrier.ConfigSetting_MaybeStrValue{},
					},
				})
			}
		},
	)

	h.loadedValues.OnChange1("1/clsp", func(clsp TimestampedValue) {
		// Causes climate card to show nothing if we send None here.

		// var value string
		// switch string(mode.value.GetMaybeStrValue()) {
		// case "cool", "auto":
		// 	value = fmt.Sprintf("%.1f", clsp.value.GetFloatValue())
		// default:
		// 	value = "None"
		// }

		// if err := h.publish("zone/1/temp_high/current", value); err != nil {
		// 	log.Printf("Error publishing: %s", err)
		// }

		if err := h.publish("zone/1/temp_high/current", fmt.Sprintf("%.1f", clsp.value.GetFloatValue())); err != nil {
			log.Printf("Error publishing: %s", err)
		}
	})

	h.loadedValues.OnChange1("1/htsp", func(htsp TimestampedValue) {
		// var value string
		// switch string(mode.value.GetMaybeStrValue()) {
		// case "heat", "auto":
		// 	value = fmt.Sprintf("%.1f", htsp.value.GetFloatValue())
		// default:
		// 	value = "None"
		// }

		// if err := h.publish("zone/1/temp_low/current", value); err != nil {
		// 	log.Printf("Error publishing: %s", err)
		// }

		if err := h.publish("zone/1/temp_low/current", fmt.Sprintf("%.1f", htsp.value.GetFloatValue())); err != nil {
			log.Printf("Error publishing: %s", err)
		}
	})

	h.loadedValues.OnChange1("1/fan", func(mode TimestampedValue) {
		if err := h.publish(
			"zone/1/fanmode/current",
			carrierFanModeToHA[string(mode.value.GetMaybeStrValue())],
		); err != nil {
			log.Printf("Error publishing: %s", err)
		}
	})

	h.loadedValues.OnChange2("opmode", "opstat", func(opmode, opstat TimestampedValue) {
		if err := h.publish(
			"action/current",
			computeCurrentAction(
				string(opmode.value.GetMaybeStrValue()),
				string(opstat.value.GetMaybeStrValue()),
			),
		); err != nil {
			log.Printf("Error publishing: %s", err)
		}
	})

	h.loadedValues.OnChange1("1/currentActivity", func(activity TimestampedValue) {
		if err := h.publish(
			"zone/1/preset_mode/current",
			string(activity.value.GetMaybeStrValue()),
		); err != nil {
			log.Printf("Error publishing: %s", err)
		}
	})

	h.loadedValues.OnChange1("system/mode", func(mode TimestampedValue) {
		if err := h.publish(
			"mode/current",
			carrierModeToHAMode[string(mode.value.GetMaybeStrValue())],
		); err != nil {
			log.Printf("Error publishing: %s", err)
		}
	})

	h.loadedValues.OnChange1("1/rh", func(rh TimestampedValue) {
		if err := h.publish(
			"zone/1/humidity/current",
			fmt.Sprintf("%f", rh.value.GetFloatValue()),
		); err != nil {
			log.Printf("Error publishing: %s", err)
		}
	})

	h.loadedValues.OnChange1("1/rt", func(rt TimestampedValue) {
		if err := h.publish(
			"zone/1/temperature/current",
			fmt.Sprintf("%.1f", rt.value.GetFloatValue()),
		); err != nil {
			log.Printf("Error publishing: %s", err)
		}
	})
}
