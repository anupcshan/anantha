# Anantha - Carrier Thermostat Local Control Toolkit

Anantha is a toolkit for local control of Carrier Infinity thermostats. This project is inspired by [Infinitude](https://github.com/nebulous/infinitude), which worked by acting as an HTTP proxy to the thermostat. Since version v4.17, these thermostats have [switched to a primarily MQTT-based protocol](https://github.com/nebulous/infinitude/issues/148), which this project aims to support.

Local control is achieved by intercepting DNS requests from the thermostat and redirecting HTTP and MQTT connections to Anantha. In addition, Anantha provides a web dashboard for interacting with the thermostat and a Home Assistant integration for controlling the thermostat from Home Assistant.

In order to use Anantha, you will need to modify the firmware for your thermostat with the `hexsed` tool. This tool will patch the firmware to include a CA certificate that the thermostat will trust, allowing Anantha to handle MQTT connections. NOTE THAT MODIFYING THE FIRMWARE WILL VERY LIKELY VOID YOUR THERMOSTAT'S WARRANTY AND MAY CAUSE OTHER ISSUES. USE AT YOUR OWN RISK. ANANTHA IS NOT AFFILIATED WITH CARRIER AND DOES NOT TAKE RESPONSIBILITY FOR ANY ISSUES THAT MAY ARISE FROM USING THIS SOFTWARE.

## How to use Anantha

### Install Go
Follow instructions in http://golang.org/dl

### Install tools

```bash
go install github.com/anupcshan/anantha/cmd/hexsed@latest github.com/anupcshan/anantha/cmd/anantha@latest
```

Installs `anantha` and `hexsed` into `$GOBIN` (typically `$HOME/go/bin`). Make sure `$GOBIN` is in your `$PATH` for the remaining steps.

`anantha` is the main server that handles all HTTP/MQTT communication from the thermostat - it can run on any machine that can be accessed by the thermostat.
`hexsed` is a tool that patches the firmware with the CA certificate. This only needs to be run once.

### Run anantha

```bash
anantha \
  -ntp-addr <NTP_IP> \                           # NTP server address accessible by the thermostat (e.g., 192.168.86.1)
  -ha-mqtt-addr <HA_MQTT_IP> \                   # MQTT server used by Home Assistant accessible from Anantha (e.g., 192.168.1.100)
  -ha-mqtt-topic-prefix <HA_MQTT_TOPIC_PREFIX> \ # e.g., hvac/carrier
  -client-id <HVAC_DEVICE_ID>                    # HVAC Device Serial ID (e.g., 4123X123456)
```

The server provides:
- DNS server on port 53 that resolves Carrier hostnames to the same IP (and optionally NTP to the provided IP)
- MQTT broker on port 8883 to handle all communication from the thermostat
- HTTP server on ports 80 and 443 (like [Infinitude](https://github.com/nebulous/infinitude)) to handle firmware updates and other requests from the thermostat
- Read-only Web dashboard on port 26268 for debugging
- Home Assistant integration with auto-discovery for an [MQTT HVAC device](https://www.home-assistant.io/integrations/climate.mqtt/)
- Optional MQTT proxying to AWS IoT (requires additional certs setup) for integration with Carrier mobile app

### Prepare and update HVAC firmware
1. Download and extract the firmware for your thermostat from the [Carrier Infinity Thermostat Firmware page](https://www.myinfinitytouch.carrier.com/Infinity/Downloads). You need a file that looks like `BINF0456.hex` (`0456` here is the firmware version, it may be different for you). This tool will patch the firmware to include a CA certificate that the thermostat will trust, allowing Anantha to handle MQTT connections.
2. Use `hexsed` to patch the firmware with the CA certificate.
```bash
hexsed -in original/BINF0456.hex -out updated/BINF0456.hex
```
3. Copy `updated/BINF0456.hex` to an SD card and flash it to your thermostat.

### Point HVAC at Anantha
Set the DNS Server in the HVAC to Anantha's IP address

### Home Assistant Integration
Turn on MQTT auto discovery.
TODO: Add more details.

### Debugging
You can access the web dashboard at `http://<ANANTHA_IP>:26268` to see the current status. See `http://<ANANTHA_IP>:26268/recent` to see a log of recently updated values (warning: there's a lot of them)

## What devices and firmware versions are known to work?

| Manufacturer | Device Model | Version|
|---|---|---|
| Carrier | SYSTXCCITC01-B | v4.47 |
| Carrier | SYSTXCCITC01-B | v4.56 |


## How does this work?

With version 4.17, the HVAC primarily communicates with Carrier servers over MQTT - Carrier uses AWS IoT to do this. Unfortunately, AWS IoT client libraries pin a small list of valid CA certificates - this prevents any MITM shenanigans, which is very commendable. However, this also means that we can't use Anantha to proxy the HVAC's MQTT traffic.

Instead, we patch the HVAC's firmware to trust Anantha's CA certificate. This allows us to use Anantha to proxy the HVAC's MQTT traffic. If you're interested, look in `cmd/cagen` for a script that generates a CA certificate that can replace an existing CA certificate while maintaining the same firmware checksum. @anupcshan spent a bunch of time in 2023 trying to figure out how to update the checksum itself and failed, so we're stuck with this solution for now.