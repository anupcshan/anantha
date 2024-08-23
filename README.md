What works
==========
- Connect from Carrier Infinity thermostat (v4.56 with cert patch) to internal MQTT broker
  - Works by setting DNS on the thermostat to point to anantha (or by setting DNS on the entire internet-isolated VLAN to it, like I do)
- Continuously poll state of thermostat and present them in a basic readonly dashboard (port 26268)
- Publish some controls via an external MQTT broker - this is usable by Home Assistant
- Proxy requests to AWS IOT for anyone who still wants to use the Carrier App - this was mainly used for reverse engineering and I would recommend not actually relying on this

What's missing (in rough priority order if I get to it)
=======================================================
- All the tooling that was used to generate certs and perform firmware mangling (its in another private repo)
- Documentation and Howto guides
- Better Home Assistant integration
  - Full auto-discovery
  - Expose all missing controls like temperature, vacation etc)
  - HACS?
- Figure out a solution for thermostat connection instability (drops connection randomly)
- Figure out a reasonable way to handle cert change between different firmware versions (today, the mangled cert is custom to a given firmware file).
  After a firmware update, we need to change the cert being presented by anantha to allow thermostat to connect.
- Better proto cleanup. We dump protobufs sent by thermostat in a directory which gets garbage collected on process startup.
  Do this on a schedule or as required.
- Weather integration with Open Meteo?
- Perform firmware patching via auto-update mechanism within the thermostat. Could be a way to onboard without needing an SD-card to flash firmware.
  Kind of dangerous given how some bits in the thermostat cannot be overwritten once set (like AWS IOT thingname, certs etc?). Could cause
  the thermostat to potentially get "bricked" if you want to use AWS IOT/Carrier API again.
- Figure out what DCMD topics are for (dealer commands?)
- Multi-zone controls (currently only does single zone control, because that's what I have)
- Humidifier/ventilator controls (I don't have these)
