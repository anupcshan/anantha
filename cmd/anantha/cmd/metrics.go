package cmd

import (
	"net/http"
	"regexp"
	"strings"
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

	wallCtrlTempGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "anantha",
		Name:      "wall_ctrl_temp",
		Help:      "Wall Control Temperature (in °F)",
	})
	loadedValues.OnChange1("sensor/wallControl/rt", func(rt TimestampedValue) {
		wallCtrlTempGauge.Set(float64(rt.value.GetFloatValue()))
	})
	prometheus.MustRegister(wallCtrlTempGauge)

	wallCtrlHumidityGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "anantha",
		Name:      "wall_ctrl_humidity",
		Help:      "Wall Control Humidity percentage (0-100)",
	})
	loadedValues.OnChange1("sensor/wallControl/rh", func(rh TimestampedValue) {
		wallCtrlHumidityGauge.Set(float64(rh.value.GetAnotherIntValue()))
	})
	prometheus.MustRegister(wallCtrlHumidityGauge)

	lastMessageReceievedTimestampGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "anantha",
		Name:      "last_msg_rcvd_timestamp_seconds",
		Help:      "Last message received from HVAC (unix time in seconds)",
	})
	loadedValues.OnAnyChange(func(timestamp time.Time) {
		lastMessageReceievedTimestampGauge.Set(float64(timestamp.Unix()))
	})
	prometheus.MustRegister(lastMessageReceievedTimestampGauge)

	var previousOpstat string
	operationStatusGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "anantha",
		Name:      "operation_status",
		Help:      "Operation status (1 if current, 0 otherwise)",
	}, []string{"status"})
	loadedValues.OnChange1("opstat", func(opstat TimestampedValue) {
		status := string(opstat.value.GetMaybeStrValue())
		if previousOpstat != "" {
			operationStatusGauge.WithLabelValues(previousOpstat).Set(0)
		}
		operationStatusGauge.WithLabelValues(status).Set(1)
		previousOpstat = status
	})
	prometheus.MustRegister(operationStatusGauge)

	yearlyUsageGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "anantha",
		Name:      "yearly_usage",
		Help:      "Yearly Usage (kWh)",
	}, []string{"year", "type"})
	loadedValues.OnChangeRegex(regexp.MustCompile("^/usage/year[0-9]*/[a-z]*$"), func(kwh TimestampedValue) {
		splits := strings.Split(kwh.value.Name, "/")
		yearlyUsageGauge.WithLabelValues(splits[2], splits[3]).Set(float64(kwh.value.GetAnotherIntValue()))
	})
	prometheus.MustRegister(yearlyUsageGauge)

	monthlyUsageGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "anantha",
		Name:      "monthly_usage",
		Help:      "Monthly Usage (kWh)",
	}, []string{"month", "type"})
	loadedValues.OnChangeRegex(regexp.MustCompile("^/usage/month[0-9]*/[a-z]*$"), func(kwh TimestampedValue) {
		splits := strings.Split(kwh.value.Name, "/")
		monthlyUsageGauge.WithLabelValues(splits[2], splits[3]).Set(float64(kwh.value.GetIntValue()))
	})
	prometheus.MustRegister(monthlyUsageGauge)

	dailyUsageGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "anantha",
		Name:      "daily_usage",
		Help:      "Daily Usage (kWh)",
	}, []string{"day", "type"})
	loadedValues.OnChangeRegex(regexp.MustCompile("^/usage/day[0-9]*/[a-z]*$"), func(kwh TimestampedValue) {
		splits := strings.Split(kwh.value.Name, "/")
		dailyUsageGauge.WithLabelValues(splits[2], splits[3]).Set(float64(kwh.value.GetIntValue()))
	})
	prometheus.MustRegister(dailyUsageGauge)

	return promhttp.Handler()
}
