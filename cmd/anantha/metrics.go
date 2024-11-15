package main

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func MetricsHandler(loadedValues *LoadedValues) http.Handler {
	outsideTempGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "anantha",
		Name:      "outside_temp",
		Help:      "Outside air temperature (in °F)",
	})
	loadedValues.OnChange1("system/oat", func(oat TimestampedValue) {
		outsideTempGauge.Set(float64(oat.value.GetIntValue()))
	})
	prometheus.MustRegister(outsideTempGauge)

	oduCoilTempGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "anantha",
		Name:      "odu_coil_temp",
		Help:      "Outdoor unit coil temperature (in °F)",
	})
	loadedValues.OnChange1("oducoiltmp", func(oducoil TimestampedValue) {
		oduCoilTempGauge.Set(float64(oducoil.value.GetIntValue()))
	})
	prometheus.MustRegister(oduCoilTempGauge)

	suctionTempGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "anantha",
		Name:      "suction_temp",
		Help:      "Suction temperature (in °F)",
	})
	loadedValues.OnChange1("sucttemp", func(suction TimestampedValue) {
		suctionTempGauge.Set(float64(suction.value.GetIntValue()))
	})
	prometheus.MustRegister(suctionTempGauge)

	dischargeTempGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "anantha",
		Name:      "discharge_temp",
		Help:      "Discharge temperature (in °F)",
	})
	loadedValues.OnChange1("dischargetmp", func(discharge TimestampedValue) {
		dischargeTempGauge.Set(float64(discharge.value.GetIntValue()))
	})
	prometheus.MustRegister(dischargeTempGauge)

	blowerRPMGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "anantha",
		Name:      "blower_rpm",
		Help:      "Blower RPM",
	})
	loadedValues.OnChange1("blwrpm", func(blw TimestampedValue) {
		blowerRPMGauge.Set(float64(blw.value.GetIntValue()))
	})
	prometheus.MustRegister(blowerRPMGauge)

	compressorRPMGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "anantha",
		Name:      "compressor_rpm",
		Help:      "Compressor RPM",
	})
	loadedValues.OnChange1("comprpm", func(blw TimestampedValue) {
		compressorRPMGauge.Set(float64(blw.value.GetIntValue()))
	})
	prometheus.MustRegister(compressorRPMGauge)

	instantPowerGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "anantha",
		Name:      "instant_power",
		Help:      "Instant Power Consumption (in W)",
	})
	loadedValues.OnChange1("instant", func(instant TimestampedValue) {
		instantPowerGauge.Set(float64(instant.value.GetIntValue()))
	})
	prometheus.MustRegister(instantPowerGauge)

	cfmGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "anantha",
		Name:      "cfm",
		Help:      "CFM",
	})
	loadedValues.OnChange1("cfm", func(cfm TimestampedValue) {
		cfmGauge.Set(float64(cfm.value.GetIntValue()))
	})
	prometheus.MustRegister(cfmGauge)

	staticPressureGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "anantha",
		Name:      "static_pressure",
		Help:      "Static Pressure (in PSI)",
	})
	loadedValues.OnChange1("statpress", func(statPress TimestampedValue) {
		staticPressureGauge.Set(float64(statPress.value.GetFloatValue()))
	})
	prometheus.MustRegister(staticPressureGauge)

	zoneTempGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "anantha",
		Name:      "zone_temp",
		Help:      "Zone temperature (in °F)",
	}, []string{"zone"})
	loadedValues.OnChange1("1/rt", func(rt TimestampedValue) {
		zoneTempGauge.WithLabelValues("1").Set(float64(rt.value.GetFloatValue()))
	})
	prometheus.MustRegister(zoneTempGauge)

	zoneHumidityGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "anantha",
		Name:      "zone_humidity",
		Help:      "Zone humidity percentage (0-100)",
	}, []string{"zone"})
	loadedValues.OnChange1("1/rh", func(rh TimestampedValue) {
		zoneHumidityGauge.WithLabelValues("1").Set(float64(rh.value.GetFloatValue()))
	})
	prometheus.MustRegister(zoneHumidityGauge)

	lastMessageReceievedTimestampGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "anantha",
		Name:      "last_msg_rcvd_timestamp_seconds",
		Help:      "Last message received from HVAC (unix time in seconds)",
	})
	loadedValues.OnAnyChange(func(timestamp time.Time) {
		lastMessageReceievedTimestampGauge.Set(float64(timestamp.Unix()))
	})
	prometheus.MustRegister(lastMessageReceievedTimestampGauge)

	return promhttp.Handler()
}
