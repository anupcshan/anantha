## Status
- [x] Connect from Carrier Infinity thermostat (v4.56 with cert patch) to internal MQTT broker
  - Works by setting DNS on the thermostat to point to anantha (or by setting DNS on the entire internet-isolated VLAN to it, like I do)
- [x] Continuously poll state of thermostat and present them in a basic readonly dashboard (port 26268)
- [x] Publish some controls via an external MQTT broker - this is usable by Home Assistant
- [x] Proxy requests to AWS IOT for anyone who still wants to use the Carrier App - this was mainly used for reverse engineering and I would recommend not actually relying on this
- [x] All the tooling that was used to generate certs and perform firmware mangling (its in another private repo)
- [x] Documentation and Howto guides
- [ ] Better Home Assistant integration
  - [x] Full auto-discovery
  - [ ] Expose all missing controls like temperature, vacation etc)
  - [ ] HACS?
- [ ] Make the web dashboard read-write
   - [ ] Calendar integration
- [x] Figure out a reasonable way to handle cert change between different firmware versions (today, the mangled cert is custom to a given firmware file).
  After a firmware update, we need to change the cert being presented by anantha to allow thermostat to connect.
- [x] Better proto cleanup. We dump protobufs sent by thermostat in a directory which gets garbage collected on process startup.
  Do this on a schedule or as required.
- [x] Weather integration with Open Meteo? Currently, we pretend to be in a California summer all year round.
- [x] Automatic full-state recovery on (re)connect. The thermostat only publishes deltas after CONNECT, so a fresh anantha
  install (or one with an empty proto cache) used to render an empty schedule and profiles until the user manually edited
  every field on the thermostat. Anantha now sends a Sparkplug B "Node Control/Rebirth" command 120s after the first
  qualifying PUBLISH, which causes the firmware to dump its full state (~2200 entries) and produces a complete dashboard
  within ~3.5 minutes of cold start. The 120s delay is necessary because the firmware silently drops rebirth requests
  received during its own NBIRTH window.
- [ ] Perform firmware patching via auto-update mechanism within the thermostat. Could be a way to onboard without needing an SD-card to flash firmware.
  Kind of dangerous given how some bits in the thermostat cannot be overwritten once set (like AWS IOT thingname, certs etc?). Could cause
  the thermostat to potentially get "bricked" if you want to use AWS IOT/Carrier API again.
- [ ] Figure out what DCMD topics are for (dealer commands?)
- [ ] Multi-zone controls (currently only does single zone control, because that's what I have)
- [ ] Humidifier/ventilator controls (I don't have these)
