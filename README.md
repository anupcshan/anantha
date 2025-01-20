# Anantha - Carrier Thermostat Local Control Toolkit

Anantha is a toolkit for local control of Carrier Infinity thermostats. This project is inspired by [Infinitude](https://github.com/nebulous/infinitude), which worked by acting as an HTTP proxy to the thermostat. Since version v4.17, these thermostats have [switched to a primarily MQTT-based protocol](https://github.com/nebulous/infinitude/issues/148), which this project aims to support.

Local control is achieved by intercepting DNS requests from the thermostat and redirecting HTTP and MQTT connections to Anantha. In addition, Anantha provides a web dashboard for interacting with the thermostat and a Home Assistant integration for controlling the thermostat from Home Assistant.

In order to use Anantha, you will need to modify the firmware for your thermostat with the `hexsed` tool. This tool will patch the firmware to include a CA certificate that the thermostat will trust, allowing Anantha to handle MQTT connections. NOTE THAT MODIFYING THE FIRMWARE WILL VERY LIKELY VOID YOUR THERMOSTAT'S WARRANTY AND MAY CAUSE OTHER ISSUES. USE AT YOUR OWN RISK. ANANTHA IS NOT AFFILIATED WITH CARRIER AND DOES NOT TAKE RESPONSIBILITY FOR ANY ISSUES THAT MAY ARISE FROM USING THIS SOFTWARE.

## Install tools

```bash
go install github.com/anupcshan/anantha/cmd/hexsed@latest github.com/anupcshan/anantha/cmd/anantha@latest
```

Installs `anantha` and `hexsed` into `$GOBIN` (typically `$HOME/go/bin`). Make sure `$GOBIN` is in your `$PATH` for the remaining steps.

## Prepare firmware
1. Download and extract the firmware for your thermostat from the [Carrier Infinity Thermostat Firmware page](https://www.myinfinitytouch.carrier.com/Infinity/Downloads). You need a file that looks like `BINF0456.hex` (`0456` here is the firmware version, it may be different for you).
2. Use `hexsed` to patch the firmware with the CA certificate.
```bash
hexsed -in original/BINF0456.hex -out updated/BINF0456.hex
```
3. Copy `updated/BINF0456.hex` to an SD card and flash it to your thermostat.

### anantha

The main server that handles all communication from the thermostat.

#### Usage

```bash
anantha [flags]

Flags:
  --ntp-addr string              NTP IPv4 Address
  --ha-mqtt-addr string          Home Assistant MQTT Host
  --ha-mqtt-topic-prefix string  Home Assistant MQTT Topic Prefix
  --reqs-dir string              Directory where request protos are stored (default "$HOME/.anantha/protos")
  --client-id string             MQTT Client ID (default "hello")
  --proxy                        Proxy requests to AWS IOT (requires valid client certificate)
```

The server provides:
- DNS interception to redirect thermostat traffic
- MQTT broker on port 8883
- Web dashboard on port 26268
- Home Assistant integration
- Optional AWS IoT proxying