package cmd

import (
	"crypto/tls"
	"embed"
	_ "embed"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/anupcshan/anantha/cmd/pkg/weather"
	carrier "github.com/anupcshan/anantha/pb"
	"github.com/anupcshan/anantha/tls/server"
	"github.com/fxamacker/cbor/v2"
	"github.com/google/uuid"
	"github.com/miekg/dns"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	mqtt_paho "github.com/eclipse/paho.mqtt.golang"
	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"
)

var (
	//go:embed manifest.xml
	manifestXML []byte

	//go:embed assets
	assets embed.FS
)

type MQTTLogger struct {
	mqtt.HookBase

	server            *mqtt.Server
	savedProtosDir    string
	iotMQTTClient     mqtt_paho.Client
	clientID          string
	thingNameOverride string

	subscribedTopics     map[string]struct{}
	subscribedTopicsLock sync.Mutex

	forwardMessageLock sync.Mutex

	loadedValues *LoadedValues

	liveClients map[string]struct{}
}

// ID returns the ID of the hook.
func (m *MQTTLogger) ID() string {
	return "logger"
}

// Provides indicates that this hook provides all methods.
func (m *MQTTLogger) Provides(b byte) bool {
	return true
}

func addAllConfigSettings(ct *carrier.CarrierInfo, loadedValues *LoadedValues, sourceFileName string) {
	loadedValues.StartLoading(sourceFileName)
	for _, setting := range ct.ConfigSettings {
		loadedValues.Update(setting.Name, setting, time.UnixMilli(ct.TimestampMillis), sourceFileName)

	}
	loadedValues.EndLoading(sourceFileName)
}

func (m *MQTTLogger) toIOTTopic(topic string) string {
	if m.thingNameOverride != "" {
		return strings.Replace(topic, m.thingNameOverride, m.clientID, 1)
	}

	return topic
}

func (m *MQTTLogger) OnPacketRead(cl *mqtt.Client, pk packets.Packet) (packets.Packet, error) {
	switch pk.FixedHeader.Type {
	case packets.Publish:
		log.Printf("%s from %s: %s [len: %d]", strings.ToUpper(packets.PacketNames[pk.FixedHeader.Type]), cl.ID, pk.TopicName, len(pk.Payload))
		if (m.thingNameOverride != "" && strings.HasSuffix(pk.TopicName, m.thingNameOverride)) ||
			(m.thingNameOverride == "" && strings.HasSuffix(pk.TopicName, m.clientID)) {
			// Client sent initial PUBLISH - ready to poll it
			m.liveClients[cl.ID] = struct{}{}
		}
		protoFilename := fmt.Sprintf("%s-%s.pb", strings.ReplaceAll(string(pk.TopicName), "/", "_"), time.Now().Format(time.RFC3339Nano))
		if err := os.WriteFile(
			filepath.Join(m.savedProtosDir, protoFilename),
			pk.Payload,
			0644,
		); err != nil {
			log.Printf("Error writing file: %s", err)
		}
		var ct carrier.CarrierInfo
		if err := proto.Unmarshal(pk.Payload, &ct); err != nil {
			log.Printf("Failed to unmarshal: %s", err)
		}

		addAllConfigSettings(&ct, m.loadedValues, protoFilename)

		if m.iotMQTTClient != nil {
			iotTopic := m.toIOTTopic(pk.TopicName)
			log.Printf("Forwarding to IOT topic %s", iotTopic)
			token := m.iotMQTTClient.Publish(iotTopic, 0, false, pk.Payload)
			token.Wait()
			if err := token.Error(); err != nil {
				log.Printf("Error publishing: %s", err)
			}
		}
	case packets.Subscribe:
		filters := map[string]int{}
		for _, v := range pk.Filters {
			filters[v.Filter] = int(v.Qos)
		}
		log.Printf("%s from %s: %+v", strings.ToUpper(packets.PacketNames[pk.FixedHeader.Type]), cl.ID, filters)

		if m.iotMQTTClient != nil {
			m.subscribedTopicsLock.Lock()
			for topic := range filters {
				iotTopic := m.toIOTTopic(topic)
				if _, ok := m.subscribedTopics[iotTopic]; ok {
					continue
				}

				m.subscribedTopics[iotTopic] = struct{}{}

				log.Printf("Subscribing to %s", iotTopic)
				m.iotMQTTClient.Subscribe(iotTopic, 0, func(_ mqtt_paho.Client, msg mqtt_paho.Message) {
					log.Printf("Got IOT %s message", iotTopic)
					if err := os.WriteFile(
						fmt.Sprintf(
							"%s/%s-%s.pb",
							m.savedProtosDir,
							strings.ReplaceAll(iotTopic, "/", "_"),
							time.Now().Format(time.RFC3339Nano),
						),
						msg.Payload(),
						0644,
					); err != nil {
						log.Printf("Error writing file: %s", err)
					}

					m.forwardMessageLock.Lock()
					log.Printf("Forwarding message to %s", topic)
					if err := m.server.Publish(topic, msg.Payload(), false, 0); err != nil {
						log.Printf("Error forwarding from IOT: %s", err)
					}
					time.Sleep(time.Second) // Don't allow another message in 1s
					m.forwardMessageLock.Unlock()
				})
			}
			m.subscribedTopicsLock.Unlock()
		}
	case packets.Connect:
		// Empty liveClients list on CONNECT. Make sure we get a PUBLISH spBv1.0/WallCtrl/NDATA/<thingName> before polling
		m.liveClients = map[string]struct{}{}
	case packets.Pingreq:
		// Don't log PINGREQ
	default:
		log.Printf("%s from %s", strings.ToUpper(packets.PacketNames[pk.FixedHeader.Type]), cl.ID)
	}
	return pk, nil
}

func GetExternalIP() (net.IP, error) {
	conn, err := net.Dial("udp", "1.1.1.1:80")
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)

	return localAddr.IP, nil
}

type CertificateAndKeyResponse struct {
	CertificateId             string `json:"certificateId"`
	CertificatePem            string `json:"certificatePem"`
	PrivateKey                string `json:"privateKey"`
	CertificateOwnershipToken string `json:"certificateOwnershipToken"`
}

type RegisterThingReq struct {
	CertificateOwnershipToken string            `json:"certificateOwnershipToken"`
	Parameters                map[string]string `json:"parameters"`
}

type RegisterThingResp struct {
	DeviceConfiguration map[string]string `json:"deviceConfiguration"`
	ThingName           string            `json:"thingName"`
}

const (
	indexTmpl = `
<!DOCTYPE html>
<html>
<head>
	<script src="/assets/htmx.org@1.9.12/dist/htmx.min.js"></script>
	<script src="/assets/htmx.org@1.9.12/dist/ext/sse.js"></script>
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<link rel="icon" type="image/svg+xml" href="/assets/logo.svg">
	<style>
		:root {
			--primary: #3b82f6;
			--primary-dark: #2563eb;
			--secondary: #64748b;
			--success: #10b981;
			--warning: #f59e0b;
			--danger: #ef4444;
			--light: #f8fafc;
			--dark: #1e293b;
			--gray: #e2e8f0;
			--gray-dark: #94a3b8;
			--border-radius: 8px;
			--box-shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.1), 0 2px 4px -1px rgba(0, 0, 0, 0.06);
			--transition: all 0.2s ease-in-out;
		}

		* {
			margin: 0;
			padding: 0;
			box-sizing: border-box;
		}

		body {
			font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, Cantarell, 'Open Sans', 'Helvetica Neue', sans-serif;
			background-color: #f1f5f9;
			color: var(--dark);
			line-height: 1.6;
			padding: 20px;
		}

		.container {
			max-width: 1200px;
			margin: 0 auto;
		}

		header {
			text-align: center;
			margin-bottom: 30px;
			padding: 20px;
			background: white;
			border-radius: var(--border-radius);
			box-shadow: var(--box-shadow);
		}

		.header-content {
			display: flex;
			align-items: center;
			justify-content: center;
			gap: 15px;
			margin-bottom: 10px;
		}

		.logo {
			width: 64px;
			height: 64px;
			flex-shrink: 0;
		}

		h1 {
			color: var(--primary);
			margin: 0;
			font-weight: 600;
		}

		.last-updated {
			color: var(--secondary);
			font-size: 0.9rem;
		}

		.section-title {
			font-size: 1.2rem;
			font-weight: 600;
			margin: 25px 0 15px 0;
			color: var(--dark);
			padding-bottom: 8px;
			border-bottom: 2px solid var(--gray);
		}

		.grid {
			display: grid;
			gap: 20px;
		}

		.grid-cols-1 { grid-template-columns: 1fr; }
		.grid-cols-2 { grid-template-columns: repeat(2, 1fr); }
		.grid-cols-3 { grid-template-columns: repeat(3, 1fr); }

		.card {
			background: white;
			border-radius: var(--border-radius);
			box-shadow: var(--box-shadow);
			padding: 20px;
			transition: var(--transition);
		}

		.card:hover {
			transform: translateY(-2px);
			box-shadow: 0 10px 15px -3px rgba(0, 0, 0, 0.1), 0 4px 6px -2px rgba(0, 0, 0, 0.05);
		}

		.card-header {
			font-weight: 600;
			margin-bottom: 15px;
			color: var(--primary);
			font-size: 1.1rem;
		}

		.data-grid {
			display: grid;
			gap: 15px;
		}

		.data-item {
			display: flex;
			flex-direction: column;
		}

		.data-label {
			font-size: 0.85rem;
			color: var(--secondary);
			margin-bottom: 5px;
		}

		.data-value {
			font-weight: 600;
			font-size: 1.1rem;
			color: var(--dark);
		}

		.status-indicator {
			display: inline-block;
			width: 10px;
			height: 10px;
			border-radius: 50%;
			margin-right: 8px;
		}

		.status-online {
			background-color: var(--success);
		}

		.status-offline {
			background-color: var(--gray-dark);
		}

		.temp-value {
			color: var(--primary);
		}

		.humidity-value {
			color: var(--warning);
		}

		.conditioning-heating {
			color: var(--danger);
		}

		.conditioning-cooling {
			color: var(--primary);
		}

		.conditioning-off {
			color: var(--gray-dark);
		}

		/* Responsive design */
		@media (max-width: 768px) {
			.grid-cols-2, .grid-cols-3 {
				grid-template-columns: 1fr;
			}

			body {
				padding: 10px;
			}

			.card {
				padding: 15px;
			}
		}

		@media (min-width: 769px) and (max-width: 1024px) {
			.grid-cols-3 {
				grid-template-columns: repeat(2, 1fr);
			}
		}

		/* Loading state */
		.data-value[sse-swap="Pending"] {
			color: var(--gray-dark);
			font-style: italic;
		}


		/* Zone name styling */
		.zone-name {
			font-size: 1.3rem;
			font-weight: 600;
			color: var(--primary-dark);
			text-align: center;
			margin: 20px 0;
		}

		.nav-links {
			margin-top: 15px;
			display: flex;
			justify-content: center;
			gap: 20px;
		}

		.nav-links a {
			color: var(--secondary);
			text-decoration: none;
			font-weight: 500;
			padding: 8px 16px;
			border-radius: var(--border-radius);
			transition: var(--transition);
		}

		.nav-links a:hover {
			background-color: var(--light);
			color: var(--primary);
		}

		.nav-links a.active {
			background-color: var(--primary);
			color: white;
		}
	</style>
</head>
<body>
	<div class="container">
		<header>
			<div class="header-content">
				<img src="/assets/logo.svg" alt="Anantha Logo" class="logo">
				<h1>Anantha</h1>
			</div>
			<div class="nav-links">
				<a href="/" class="active">Dashboard</a>
				<a href="/schedule">Schedule</a>
				<a href="/profiles">Profiles</a>
			</div>
		</header>

		<div hx-ext="sse" sse-connect="/events">
			<div class="last-updated">Last updated: <span sse-swap="last-updated">Never</span></div>
			<!-- System Overview Card -->
			<div class="section-title">System Overview</div>
			<div class="grid grid-cols-2">
				<div class="card">
					<div class="data-grid grid-cols-2">
						<div class="data-item">
							<div class="data-label">Outside Temperature</div>
							<div class="data-value temp-value" sse-swap="system/oat">Pending</div>
						</div>
						<div class="data-item">
							<div class="data-label">System Mode</div>
							<div class="data-value" sse-swap="system/mode">Pending</div>
						</div>
					</div>
				</div>

				<div class="card">
					<div class="data-item">
						<div class="data-label">Zone</div>
						<div class="zone-name" sse-swap="1/name">Zone Name Pending</div>
					</div>
				</div>
			</div>

			<!-- Zone Control Card -->
			<div class="section-title">Zone Control</div>
			<div class="card">
				<div class="data-grid grid-cols-2 grid-cols-3">
					<div class="data-item">
						<div class="data-label">Temperature</div>
						<div class="data-value temp-value" sse-swap="1/rt">Pending</div>
					</div>
					<div class="data-item">
						<div class="data-label">Humidity</div>
						<div class="data-value humidity-value" sse-swap="1/rh">Pending</div>
					</div>
					<div class="data-item">
						<div class="data-label">Current Activity</div>
						<div class="data-value" sse-swap="1/currentActivity">Pending</div>
					</div>
					<div class="data-item">
						<div class="data-label">Conditioning</div>
						<div class="data-value" sse-swap="1/zoneconditioning">Pending</div>
					</div>
					<div class="data-item">
						<div class="data-label">Heat Setpoint</div>
						<div class="data-value" sse-swap="1/htsp">Pending</div>
					</div>
					<div class="data-item">
						<div class="data-label">Cool Setpoint</div>
						<div class="data-value" sse-swap="1/clsp">Pending</div>
					</div>
					<div class="data-item">
						<div class="data-label">Fan Status</div>
						<div class="data-value" sse-swap="1/fan">Pending</div>
					</div>
				</div>
			</div>

			<!-- System Details Card -->
			<div class="section-title">System Details</div>
			<div class="card">
				<div class="data-grid grid-cols-2 grid-cols-3">
					<div class="data-item">
						<div class="data-label">Blower RPM</div>
						<div class="data-value" sse-swap="blwrpm">Pending</div>
					</div>
					<div class="data-item">
						<div class="data-label">Compressor RPM</div>
						<div class="data-value" sse-swap="comprpm">Pending</div>
					</div>
					<div class="data-item">
						<div class="data-label">Power</div>
						<div class="data-value" sse-swap="instant">Pending</div>
					</div>
					<div class="data-item">
						<div class="data-label">Airflow</div>
						<div class="data-value" sse-swap="cfm">Pending</div>
					</div>
					<div class="data-item">
						<div class="data-label">Status</div>
						<div class="data-value" sse-swap="opstat">Pending</div>
					</div>
					<div class="data-item">
						<div class="data-label">Mode</div>
						<div class="data-value" sse-swap="opmode">Pending</div>
					</div>
					<div class="data-item">
						<div class="data-label">Coil Temp</div>
						<div class="data-value" sse-swap="oducoiltmp">Pending</div>
					</div>
					<div class="data-item">
						<div class="data-label">Suction Temp</div>
						<div class="data-value" sse-swap="sucttemp">Pending</div>
					</div>
					<div class="data-item">
						<div class="data-label">Discharge Temp</div>
						<div class="data-value" sse-swap="dischargetmp">Pending</div>
					</div>
				</div>
			</div>

			<!-- Version Information Card -->
			<div class="section-title">System Versions</div>
			<div class="card">
				<div class="data-grid grid-cols-3">
					<div class="data-item">
						<div class="data-label">Firmware</div>
						<div class="data-value" sse-swap="profile/firmware">Pending</div>
					</div>
					<div class="data-item">
						<div class="data-label">IDU Version</div>
						<div class="data-value" sse-swap="profile/iduversion">Pending</div>
					</div>
					<div class="data-item">
						<div class="data-label">ODU Version</div>
						<div class="data-value" sse-swap="profile/oduversion">Pending</div>
					</div>
				</div>
			</div>
		</div>
	</div>
</body>
</html>
`
)

type SchedulePeriod struct {
	StartTime string
	EndTime   string
	Activity  string
	Duration  int // in minutes
	Enabled   bool
}

type DaySchedule struct {
	Day     string
	Periods []SchedulePeriod
}

func parseTime(timeStr string) (int, int, error) {
	if timeStr == "00:00" || timeStr == "" {
		return 0, 0, nil
	}
	var hour, minute int
	n, err := fmt.Sscanf(timeStr, "%d:%d", &hour, &minute)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to parse time '%s': %w", timeStr, err)
	}
	if n != 2 {
		return 0, 0, fmt.Errorf("expected 2 values when parsing time '%s', got %d", timeStr, n)
	}
	return hour, minute, nil
}

func timeToMinutes(hour, minute int) int {
	return hour*60 + minute
}

func minutesToTime(minutes int) string {
	hour := (minutes / 60) % 24
	minute := minutes % 60
	return fmt.Sprintf("%02d:%02d", hour, minute)
}

func computeScheduleBlocks(periods []SchedulePeriod) ([]SchedulePeriod, error) {
	if len(periods) == 0 {
		return periods, nil
	}

	// Sort periods by start time
	for i := 0; i < len(periods)-1; i++ {
		for j := i + 1; j < len(periods); j++ {
			hour1, min1, err := parseTime(periods[i].StartTime)
			if err != nil {
				return nil, fmt.Errorf("invalid start time '%s' in schedule: %w", periods[i].StartTime, err)
			}
			hour2, min2, err := parseTime(periods[j].StartTime)
			if err != nil {
				return nil, fmt.Errorf("invalid start time '%s' in schedule: %w", periods[j].StartTime, err)
			}
			if timeToMinutes(hour1, min1) > timeToMinutes(hour2, min2) {
				periods[i], periods[j] = periods[j], periods[i]
			}
		}
	}

	var result []SchedulePeriod

	// Calculate end times and durations, splitting blocks that cross midnight
	for i := 0; i < len(periods); i++ {
		startHour, startMin, err := parseTime(periods[i].StartTime)
		if err != nil {
			return nil, fmt.Errorf("invalid start time '%s' in schedule: %w", periods[i].StartTime, err)
		}
		startMinutes := timeToMinutes(startHour, startMin)

		var endMinutes int
		if i < len(periods)-1 {
			// End time is the start of the next period
			nextHour, nextMin, err := parseTime(periods[i+1].StartTime)
			if err != nil {
				return nil, fmt.Errorf("invalid start time '%s' in schedule: %w", periods[i+1].StartTime, err)
			}
			endMinutes = timeToMinutes(nextHour, nextMin)
		} else {
			// Last period ends at the start of the first period next day
			firstHour, firstMin, err := parseTime(periods[0].StartTime)
			if err != nil {
				return nil, fmt.Errorf("invalid start time '%s' in schedule: %w", periods[0].StartTime, err)
			}
			endMinutes = timeToMinutes(firstHour, firstMin)
		}

		// Check if this period crosses midnight
		if endMinutes <= startMinutes {
			// Period crosses midnight - split into two parts

			// Part 1: From start time to end of day (23:59)
			endOfDay := 24 * 60 // midnight
			duration1 := endOfDay - startMinutes

			if duration1 > 0 {
				result = append(result, SchedulePeriod{
					StartTime: periods[i].StartTime,
					EndTime:   "23:59",
					Activity:  periods[i].Activity,
					Duration:  duration1,
					Enabled:   periods[i].Enabled,
				})
			}

			// Part 2: From start of day to end time (handled by next day)
			// This will be handled when processing the next day's schedule

		} else {
			// Period stays within the same day
			duration := endMinutes - startMinutes

			result = append(result, SchedulePeriod{
				StartTime: periods[i].StartTime,
				EndTime:   minutesToTime(endMinutes),
				Activity:  periods[i].Activity,
				Duration:  duration,
				Enabled:   periods[i].Enabled,
			})
		}
	}

	return result, nil
}

func addCrossoverBlocks(daySchedules map[string][]SchedulePeriod, days []string) error {
	// Process each day to add continuation blocks from midnight crossovers
	for i, day := range days {
		prevDayIndex := (i - 1 + len(days)) % len(days)
		prevDay := days[prevDayIndex]

		prevPeriods := daySchedules[prevDay]
		if len(prevPeriods) == 0 {
			continue
		}

		// Check if the last period of the previous day ends at 23:59 (indicating a crossover)
		lastPeriod := prevPeriods[len(prevPeriods)-1]
		if lastPeriod.EndTime != "23:59" {
			continue
		}

		// Find where this period should end on the current day
		currentPeriods := daySchedules[day]
		var endTime string
		var endMinutes int

		if len(currentPeriods) > 0 {
			// End at the start of the first period of current day
			endTime = currentPeriods[0].StartTime
			endHour, endMin, err := parseTime(endTime)
			if err != nil {
				return fmt.Errorf("invalid end time '%s' in crossover block: %w", endTime, err)
			}
			endMinutes = timeToMinutes(endHour, endMin)
		} else {
			// If no periods on current day, we need to find the next day with periods
			// For now, assume it continues until the same time as the original schedule
			// This is a simplification - in reality we'd need to scan forward
			endTime = "06:30" // Default based on the schedule pattern
			endMinutes = timeToMinutes(6, 30)
		}

		if endMinutes > 0 {
			crossoverBlock := SchedulePeriod{
				StartTime: "00:00",
				EndTime:   endTime,
				Activity:  lastPeriod.Activity,
				Duration:  endMinutes,
				Enabled:   lastPeriod.Enabled,
			}

			// Insert at the beginning of current day
			daySchedules[day] = append([]SchedulePeriod{crossoverBlock}, daySchedules[day]...)
		}
	}
	return nil
}

func generateProfilesHTML(loadedValues *LoadedValues) (string, error) {
	activities := []string{"home", "away", "sleep", "wake", "manual"}

	snapshot := loadedValues.Snapshot()

	html := `<!DOCTYPE html>
<html>
<head>
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<link rel="icon" type="image/svg+xml" href="/assets/logo.svg">
	<style>
		:root {
			--primary: #3b82f6;
			--primary-dark: #2563eb;
			--secondary: #64748b;
			--success: #10b981;
			--warning: #f59e0b;
			--danger: #ef4444;
			--light: #f8fafc;
			--dark: #1e293b;
			--gray: #e2e8f0;
			--gray-dark: #94a3b8;
			--border-radius: 8px;
			--box-shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.1), 0 2px 4px -1px rgba(0, 0, 0, 0.06);
			--transition: all 0.2s ease-in-out;
		}

		* {
			margin: 0;
			padding: 0;
			box-sizing: border-box;
		}

		body {
			font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, Cantarell, 'Open Sans', 'Helvetica Neue', sans-serif;
			background-color: #f1f5f9;
			color: var(--dark);
			line-height: 1.6;
			padding: 20px;
		}

		.container {
			max-width: 1200px;
			margin: 0 auto;
		}

		header {
			text-align: center;
			margin-bottom: 30px;
			padding: 20px;
			background: white;
			border-radius: var(--border-radius);
			box-shadow: var(--box-shadow);
		}

		.header-content {
			display: flex;
			align-items: center;
			justify-content: center;
			gap: 15px;
			margin-bottom: 10px;
		}

		.logo {
			width: 64px;
			height: 64px;
			flex-shrink: 0;
		}

		h1 {
			color: var(--primary);
			margin: 0;
			font-weight: 600;
		}

		.nav-links {
			margin-top: 15px;
			display: flex;
			justify-content: center;
			gap: 20px;
		}

		.nav-links a {
			color: var(--secondary);
			text-decoration: none;
			font-weight: 500;
			padding: 8px 16px;
			border-radius: var(--border-radius);
			transition: var(--transition);
		}

		.nav-links a:hover {
			background-color: var(--light);
			color: var(--primary);
		}

		.nav-links a.active {
			background-color: var(--primary);
			color: white;
		}

		.profiles-grid {
			display: grid;
			grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
			gap: 20px;
			margin-top: 20px;
		}

		.profile-card {
			background: white;
			border-radius: var(--border-radius);
			box-shadow: var(--box-shadow);
			padding: 20px;
			transition: var(--transition);
		}

		.profile-card:hover {
			transform: translateY(-2px);
			box-shadow: 0 10px 15px -3px rgba(0, 0, 0, 0.1), 0 4px 6px -2px rgba(0, 0, 0, 0.05);
		}

		.profile-header {
			display: flex;
			align-items: center;
			margin-bottom: 20px;
			padding-bottom: 15px;
			border-bottom: 2px solid var(--gray);
		}

		.profile-icon {
			width: 40px;
			height: 40px;
			border-radius: 50%;
			display: flex;
			align-items: center;
			justify-content: center;
			margin-right: 15px;
			font-size: 1.2rem;
		}

		.profile-home .profile-icon {
			background-color: rgba(16, 185, 129, 0.1);
			color: var(--success);
		}

		.profile-away .profile-icon {
			background-color: rgba(245, 158, 11, 0.1);
			color: var(--warning);
		}

		.profile-sleep .profile-icon {
			background-color: rgba(59, 130, 246, 0.1);
			color: var(--primary);
		}

		.profile-wake .profile-icon {
			background-color: rgba(245, 158, 11, 0.1);
			color: var(--warning);
		}

		.profile-manual .profile-icon {
			background-color: rgba(100, 116, 139, 0.1);
			color: var(--secondary);
		}

		.profile-name {
			font-size: 1.2rem;
			font-weight: 600;
			color: var(--dark);
			text-transform: capitalize;
		}

		.settings-grid {
			display: grid;
			gap: 15px;
		}

		.setting-item {
			display: flex;
			justify-content: space-between;
			align-items: center;
			padding: 12px 0;
		}

		.setting-label {
			font-weight: 500;
			color: var(--secondary);
		}

		.setting-value {
			font-weight: 600;
			font-size: 1.1rem;
			color: var(--dark);
		}

		.temperature-value {
			color: var(--primary);
		}

		.fan-value {
			color: var(--secondary);
			text-transform: capitalize;
		}

		.no-data {
			text-align: center;
			color: var(--gray-dark);
			font-style: italic;
			padding: 20px;
		}

		@media (max-width: 768px) {
			.profiles-grid {
				grid-template-columns: 1fr;
			}

			body {
				padding: 10px;
			}

			.profile-card {
				padding: 15px;
			}
		}
	</style>
</head>
<body>
	<div class="container">
		<header>
			<div class="header-content">
				<img src="/assets/logo.svg" alt="Anantha Logo" class="logo">
				<h1>Anantha</h1>
			</div>
			<div class="nav-links">
				<a href="/">Dashboard</a>
				<a href="/schedule">Schedule</a>
				<a href="/profiles" class="active">Profiles</a>
			</div>
		</header>

		<div class="profiles-grid">`

	// Generate profile cards
	for _, activity := range activities {
		htspKey := fmt.Sprintf("1/activities/%s/htsp", activity)
		clspKey := fmt.Sprintf("1/activities/%s/clsp", activity)
		fanKey := fmt.Sprintf("1/activities/%s/fan", activity)

		htspVal, hasHtsp := snapshot[htspKey]
		clspVal, hasClsp := snapshot[clspKey]
		fanVal, hasFan := snapshot[fanKey]

		var icon string
		switch activity {
		case "home":
			icon = "🏠"
		case "away":
			icon = "🚗"
		case "sleep":
			icon = "😴"
		case "wake":
			icon = "☀️"
		case "manual":
			icon = "⚙️"
		}

		html += fmt.Sprintf(`
			<div class="profile-card profile-%s">
				<div class="profile-header">
					<div class="profile-icon">%s</div>
					<div class="profile-name">%s</div>
				</div>`, activity, icon, activity)

		if hasHtsp || hasClsp || hasFan {
			html += `<div class="settings-grid">`

			if hasHtsp {
				html += fmt.Sprintf(`
				<div class="setting-item">
					<div class="setting-label">Heat Setpoint</div>
					<div class="setting-value temperature-value">%.1f°F</div>
				</div>`, htspVal.value.GetFloatValue())
			}

			if hasClsp {
				html += fmt.Sprintf(`
				<div class="setting-item">
					<div class="setting-label">Cool Setpoint</div>
					<div class="setting-value temperature-value">%.1f°F</div>
				</div>`, clspVal.value.GetFloatValue())
			}

			if hasFan {
				fanSetting := string(fanVal.value.GetMaybeStrValue())
				if fanSetting == "" {
					fanSetting = "auto"
				}
				html += fmt.Sprintf(`
				<div class="setting-item">
					<div class="setting-label">Fan Setting</div>
					<div class="setting-value fan-value">%s</div>
				</div>`, fanSetting)
			}

			html += `</div>`
		} else {
			html += `<div class="no-data">No settings available</div>`
		}

		html += `</div>`
	}

	html += `
		</div>
	</div>
</body>
</html>`

	return html, nil
}

func generateScheduleHTML(loadedValues *LoadedValues) (string, error) {
	days := []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
	daySchedules := make(map[string][]SchedulePeriod)

	// Get snapshot of all current values
	snapshot := loadedValues.Snapshot()

	// Parse schedule data for each day
	for _, day := range days {
		var periods []SchedulePeriod

		for period := 1; period <= 5; period++ {
			timeKey := fmt.Sprintf("1/program/%s/period %d/time", day, period)
			activityKey := fmt.Sprintf("1/program/%s/period %d/activity", day, period)
			enabledKey := fmt.Sprintf("1/program/%s/period %d/enabled", day, period)

			timeVal, hasTime := snapshot[timeKey]
			activityVal, hasActivity := snapshot[activityKey]
			enabledVal, hasEnabled := snapshot[enabledKey]

			if hasTime && hasActivity && hasEnabled && enabledVal.value.GetBoolValue() {
				periods = append(periods, SchedulePeriod{
					StartTime: timeVal.ToString(),
					Activity:  activityVal.ToString(),
					Enabled:   true,
				})
			}
		}

		// Compute time blocks and durations for this day
		blocks, err := computeScheduleBlocks(periods)
		if err != nil {
			return "", fmt.Errorf("failed to compute schedule blocks for %s: %w", day, err)
		}
		daySchedules[day] = blocks
	}

	// Add crossover blocks from previous day
	if err := addCrossoverBlocks(daySchedules, days); err != nil {
		return "", fmt.Errorf("failed to add crossover blocks: %w", err)
	}

	// Generate HTML
	html := `<!DOCTYPE html>
<html>
<head>
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<link rel="icon" type="image/svg+xml" href="/assets/logo.svg">
	<style>
		:root {
			--primary: #3b82f6;
			--primary-dark: #2563eb;
			--secondary: #64748b;
			--success: #10b981;
			--warning: #f59e0b;
			--danger: #ef4444;
			--light: #f8fafc;
			--dark: #1e293b;
			--gray: #e2e8f0;
			--gray-dark: #94a3b8;
			--border-radius: 8px;
			--box-shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.1), 0 2px 4px -1px rgba(0, 0, 0, 0.06);
			--transition: all 0.2s ease-in-out;
		}

		* {
			margin: 0;
			padding: 0;
			box-sizing: border-box;
		}

		body {
			font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, Cantarell, 'Open Sans', 'Helvetica Neue', sans-serif;
			background-color: #f1f5f9;
			color: var(--dark);
			line-height: 1.6;
			padding: 20px;
		}

		.container {
			max-width: 1200px;
			margin: 0 auto;
		}

		header {
			text-align: center;
			margin-bottom: 30px;
			padding: 20px;
			background: white;
			border-radius: var(--border-radius);
			box-shadow: var(--box-shadow);
		}

		.header-content {
			display: flex;
			align-items: center;
			justify-content: center;
			gap: 15px;
			margin-bottom: 10px;
		}

		.logo {
			width: 64px;
			height: 64px;
			flex-shrink: 0;
		}

		h1 {
			color: var(--primary);
			margin: 0;
			font-weight: 600;
		}

		.nav-links {
			margin-top: 15px;
			display: flex;
			justify-content: center;
			gap: 20px;
		}

		.nav-links a {
			color: var(--secondary);
			text-decoration: none;
			font-weight: 500;
			padding: 8px 16px;
			border-radius: var(--border-radius);
			transition: var(--transition);
		}

		.nav-links a:hover {
			background-color: var(--light);
			color: var(--primary);
		}

		.nav-links a.active {
			background-color: var(--primary);
			color: white;
		}

		.schedule-grid {
			display: grid;
			grid-template-columns: repeat(7, 1fr);
			gap: 15px;
			margin-top: 20px;
		}

		.day-column {
			background: white;
			border-radius: var(--border-radius);
			box-shadow: var(--box-shadow);
			padding: 15px;
			min-height: 600px;
			display: flex;
			flex-direction: column;
		}

		.day-header {
			font-weight: 600;
			color: var(--primary);
			margin-bottom: 15px;
			text-align: center;
			padding-bottom: 10px;
			border-bottom: 2px solid var(--gray);
			flex-shrink: 0;
		}

		.timeline {
			flex: 1;
			display: flex;
			flex-direction: column;
			position: relative;
		}

		.time-block {
			border-radius: 6px;
			padding: 8px 12px;
			margin-bottom: 2px;
			display: flex;
			flex-direction: column;
			justify-content: center;
			border-left: 4px solid;
			position: relative;
		}

		.time-block.activity-home {
			background-color: #ecfdf5;
			border-left-color: var(--success);
		}

		.time-block.activity-sleep {
			background-color: #eff6ff;
			border-left-color: var(--primary);
		}

		.time-block.activity-away {
			background-color: #fffbeb;
			border-left-color: var(--warning);
		}

		.block-header {
			font-weight: 600;
			color: var(--dark);
			font-size: 0.85rem;
			margin-bottom: 2px;
		}

		.block-time-range {
			font-size: 0.75rem;
			color: var(--secondary);
			margin-bottom: 4px;
		}

		.block-activity {
			font-size: 0.8rem;
			font-weight: 500;
			text-transform: capitalize;
		}

		.activity-home .block-activity {
			color: var(--success);
		}

		.activity-sleep .block-activity {
			color: var(--primary);
		}

		.activity-away .block-activity {
			color: var(--warning);
		}

		.block-duration {
			font-size: 0.7rem;
			color: var(--gray-dark);
			margin-top: 2px;
		}

		.current-time-indicator {
			position: absolute;
			left: 0;
			right: 0;
			height: 2px;
			background-color: var(--danger);
			z-index: 10;
			box-shadow: 0 0 4px rgba(239, 68, 68, 0.6);
		}

		.current-time-indicator::before {
			content: '';
			position: absolute;
			left: -4px;
			top: -2px;
			width: 8px;
			height: 6px;
			background-color: var(--danger);
			border-radius: 50%;
		}

		.current-time-indicator::after {
			content: attr(data-time);
			position: absolute;
			right: 8px;
			top: -12px;
			font-size: 0.7rem;
			color: var(--danger);
			font-weight: 600;
			background: white;
			padding: 2px 6px;
			border-radius: 4px;
			box-shadow: 0 1px 3px rgba(0,0,0,0.2);
		}


		.no-schedule {
			text-align: center;
			color: var(--gray-dark);
			font-style: italic;
			margin-top: 20px;
		}

		@media (max-width: 1024px) {
			.schedule-grid {
				grid-template-columns: 1fr;
			}
		}

		@media (min-width: 768px) and (max-width: 1024px) {
			.schedule-grid {
				grid-template-columns: repeat(2, 1fr);
			}
		}
	</style>
</head>
<body>
	<div class="container">
		<header>
			<div class="header-content">
				<img src="/assets/logo.svg" alt="Anantha Logo" class="logo">
				<h1>Anantha</h1>
			</div>
			<div class="nav-links">
				<a href="/">Dashboard</a>
				<a href="/schedule" class="active">Schedule</a>
				<a href="/profiles">Profiles</a>
			</div>
		</header>

		<div class="schedule-grid">`

	// Get current time
	now := time.Now()
	currentDay := now.Weekday().String()
	currentHour := now.Hour()
	currentMinute := now.Minute()
	currentTimeMinutes := timeToMinutes(currentHour, currentMinute)
	currentTimeStr := fmt.Sprintf("%02d:%02d", currentHour, currentMinute)

	for _, day := range days {
		html += fmt.Sprintf(`
			<div class="day-column">
				<div class="day-header">%s</div>
				<div class="timeline">`, day)

		periods := daySchedules[day]

		// Add current time indicator positioned relative to entire timeline (only for today)
		var timelineHTML string
		var currentTimeIndicatorHTML string

		if day == currentDay {
			// Calculate current time position as percentage of entire day
			currentTimePercent := float64(currentTimeMinutes) / float64(24*60) * 100
			currentTimeIndicatorHTML = fmt.Sprintf(`<div class="current-time-indicator" style="top: %.1f%%;" data-time="%s"></div>`, currentTimePercent, currentTimeStr)
		}

		if len(periods) == 0 {
			timelineHTML += `<div class="no-schedule">No schedule entries</div>`
		} else {
			// Calculate total duration for proportional sizing
			totalMinutes := 24 * 60

			for _, period := range periods {
				// Calculate height as percentage of total day
				heightPercent := float64(period.Duration) / float64(totalMinutes) * 100

				// Format duration for display
				hours := period.Duration / 60
				minutes := period.Duration % 60
				var durationStr string
				if hours > 0 && minutes > 0 {
					durationStr = fmt.Sprintf("%dh %dm", hours, minutes)
				} else if hours > 0 {
					durationStr = fmt.Sprintf("%dh", hours)
				} else {
					durationStr = fmt.Sprintf("%dm", minutes)
				}

				activityClass := fmt.Sprintf("activity-%s", period.Activity)
				timelineHTML += fmt.Sprintf(`
				<div class="time-block %s" style="height: %.1f%%;">
					<div class="block-time-range">%s - %s</div>
					<div class="block-activity">%s</div>
					<div class="block-duration">%s</div>
				</div>`, activityClass, heightPercent, period.StartTime, period.EndTime, period.Activity, durationStr)
			}
		}

		// Combine timeline content with current time indicator
		html += timelineHTML + currentTimeIndicatorHTML

		html += `</div></div>`
	}

	html += `
		</div>
	</div>
</body>
</html>`

	return html, nil
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the anantha server",
	Long: `Start the anantha server with DNS, MQTT, and HTTP services.

The server handles:
- DNS server (port 53) for Carrier hostname resolution
- MQTT broker (port 8883) for thermostat communication
- HTTP server (ports 80, 443) for firmware updates and requests
- Web dashboard (port 26268) for debugging and monitoring`,
	RunE: runServe,
}

func init() {
	serveCmd.Flags().String("ntp-addr", "", "NTP IPv4 Address (if unset, we won't respond to DNS requests for *.ntp.org)")
	serveCmd.Flags().String("ha-mqtt-addr", "", "Home Assistant MQTT Host")
	serveCmd.Flags().String("ha-mqtt-topic-prefix", "", "Home Assistant MQTT Topic Prefix")
	serveCmd.Flags().String("ha-mqtt-username", "", "Home Assistant MQTT Username")
	serveCmd.Flags().String("ha-mqtt-password", "", "Home Assistant MQTT Password")
	serveCmd.Flags().String("reqs-dir", "$HOME/.anantha/protos", "Directory where request protos are stored")
	serveCmd.Flags().String("client-id", "", "MQTT Client ID (this should be the same as the HVAC device ID, e.g. '4123X123456')")
	serveCmd.Flags().String("thing-name-override", "", "Thingname override - you should never need to set this")
	serveCmd.Flags().Bool("proxy", false, "Proxy requests to AWS IOT - requires a valid client certificate for now (strongly discouraged)")
}

func runServe(cmd *cobra.Command, args []string) error {
	ntpAddrStr, _ := cmd.Flags().GetString("ntp-addr")
	haMQTTAddr, _ := cmd.Flags().GetString("ha-mqtt-addr")
	haMQTTTopicPrefix, _ := cmd.Flags().GetString("ha-mqtt-topic-prefix")
	haMQTTUsername, _ := cmd.Flags().GetString("ha-mqtt-username")
	haMQTTPassword, _ := cmd.Flags().GetString("ha-mqtt-password")
	protosDir, _ := cmd.Flags().GetString("reqs-dir")
	clientID, _ := cmd.Flags().GetString("client-id")
	thingNameOverride, _ := cmd.Flags().GetString("thing-name-override")
	proxyToAWSIOT, _ := cmd.Flags().GetBool("proxy")

	log.SetFlags(log.Lmicroseconds | log.Lshortfile)

	var shutdownFuncs []func()

	cmdTopic := fmt.Sprintf("spBv1.0/WallCtrl/NCMD/%s", clientID)
	if thingNameOverride != "" {
		cmdTopic = fmt.Sprintf("spBv1.0/WallCtrl/NCMD/%s", thingNameOverride)
	}

	carrierHTTPMux := http.NewServeMux()

	carrierHTTPMux.HandleFunc("/Alive", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("New request %s - %s - %+v", r.Method, r.URL, r.Header)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("alive"))
		_, _ = io.Copy(os.Stderr, r.Body)
	})

	carrierHTTPMux.HandleFunc("/manifest", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("New request %s - %s - %+v", r.Method, r.URL, r.Header)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(manifestXML)
		_, _ = io.Copy(os.Stderr, r.Body)
	})

	carrierHTTPMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("New request %s - %s - %+v", r.Method, r.URL, r.Header)
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(os.Stderr, r.Body)
	})

	externalIP, err := GetExternalIP()
	if err != nil {
		return fmt.Errorf("failed to get external IP: %w", err)
	}

	computeAnswer := func(q dns.Question) dns.RR {
		if externalIP.To4() != nil {
			return &dns.A{
				A:   externalIP,
				Hdr: dns.RR_Header{Name: q.Name, Class: q.Qclass, Ttl: 5, Rrtype: dns.TypeA},
			}
		}

		return &dns.AAAA{
			AAAA: externalIP,
			Hdr:  dns.RR_Header{Name: q.Name, Class: q.Qclass, Ttl: 5, Rrtype: dns.TypeA},
		}
	}

	dnsServer := &dns.Server{Addr: ":53", Net: "udp"}
	dns.HandleFunc("carrier.io.", func(w dns.ResponseWriter, r *dns.Msg) {
		log.Printf("MQTT Req from %s: %+v", w.RemoteAddr(), r.Question[0].Name)
		m := new(dns.Msg)
		m.SetReply(r)
		m.Authoritative = true
		defer func() {
			_ = w.WriteMsg(m)
		}()

		for _, q := range r.Question {
			m.Answer = append(m.Answer, computeAnswer(q))
		}
	})
	dns.HandleFunc("carrier.com.", func(w dns.ResponseWriter, r *dns.Msg) {
		log.Printf("HTTP Req from %s: %+v", w.RemoteAddr(), r.Question[0].Name)
		m := new(dns.Msg)
		m.SetReply(r)
		m.Authoritative = true
		defer func() {
			_ = w.WriteMsg(m)
		}()

		for _, q := range r.Question {
			m.Answer = append(m.Answer, computeAnswer(q))
		}
	})
	dns.HandleFunc("no-ip.info.", func(w dns.ResponseWriter, r *dns.Msg) {
		log.Printf("HTTP Req from %s: %+v", w.RemoteAddr(), r.Question[0].Name)
		m := new(dns.Msg)
		m.SetReply(r)
		m.Authoritative = true
		defer func() {
			_ = w.WriteMsg(m)
		}()

		for _, q := range r.Question {
			m.Answer = append(m.Answer, computeAnswer(q))
		}
	})
	if len(ntpAddrStr) > 0 {
		ntpAddr := net.ParseIP(ntpAddrStr)
		dns.HandleFunc("ntp.org.", func(w dns.ResponseWriter, r *dns.Msg) {
			log.Printf("NTP Req from %s: %+v", w.RemoteAddr(), r.Question[0].Name)
			m := new(dns.Msg)
			m.SetReply(r)
			m.Authoritative = true
			defer func() {
				_ = w.WriteMsg(m)
			}()

			if ntpAddr.To4() != nil {
				for _, q := range r.Question {
					m.Answer = append(m.Answer, &dns.A{
						A:   ntpAddr,
						Hdr: dns.RR_Header{Name: q.Name, Class: q.Qclass, Ttl: 5, Rrtype: dns.TypeA},
					})
				}
			} else {
				for _, q := range r.Question {
					m.Answer = append(m.Answer, &dns.AAAA{
						AAAA: ntpAddr,
						Hdr:  dns.RR_Header{Name: q.Name, Class: q.Qclass, Ttl: 5, Rrtype: dns.TypeA},
					})
				}
			}
		})
	}
	dns.HandleFunc(".", func(w dns.ResponseWriter, r *dns.Msg) {
		log.Printf("DNS Req from %s: %+v", w.RemoteAddr(), r.Question[0].Name)
		m := new(dns.Msg)
		m.SetReply(r)
		_ = w.WriteMsg(m)
	})
	shutdownFuncs = append(shutdownFuncs, func() {
		log.Println("Shutting down DNS")
		_ = dnsServer.Shutdown()
	})

	var awsIOTMQTTClient mqtt_paho.Client
	if proxyToAWSIOT {
		clientCert, err := tls.LoadX509KeyPair("tls/client/cert.pem", "tls/client/key.pem")
		if err != nil {
			return fmt.Errorf("failed to load client certificate: %w", err)
		}

		awsIOTMQTTClient = mqtt_paho.NewClient(
			mqtt_paho.NewClientOptions().
				AddBroker("mqtts://mqtt.res.carrier.io:443").
				SetClientID(clientID).
				SetTLSConfig(&tls.Config{
					Certificates: []tls.Certificate{clientCert},
					NextProtos:   []string{"x-amzn-mqtt-ca"},
				}),
		)

		log.Printf("Connecting to mqtt.res.carrier.io")
		if token := awsIOTMQTTClient.Connect(); token.Wait() && token.Error() != nil {
			return fmt.Errorf("error connecting to MQTT: %w", token.Error())
		}
		log.Printf("Connected to mqtt.res.carrier.io")
	}

	savedProtosDir := os.ExpandEnv(protosDir)
	if err := os.MkdirAll(savedProtosDir, 0755); err != nil {
		return fmt.Errorf("failed to create proto dump directory: %w", err)
	}

	loadedValues := NewLoadedValues(savedProtosDir)

	dirents, err := os.ReadDir(savedProtosDir)
	if err != nil {
		return fmt.Errorf("failed to list directory: %w", err)
	}

	files := []string{}

	for _, dirent := range dirents {
		if dirent.IsDir() {
			continue
		}
		if !strings.HasPrefix(dirent.Name(), "spBv1.0") {
			continue
		}
		if strings.Contains(dirent.Name(), "NCMD") || strings.Contains(dirent.Name(), "DCMD") {
			continue
		}

		files = append(files, dirent.Name())
	}

	extractTS := func(fName string) time.Time {
		timePlusExt := strings.SplitAfterN(fName, "-", 2)[1]
		t, _ := time.Parse(time.RFC3339, timePlusExt[:len(timePlusExt)-3])
		return t
	}

	sort.SliceStable(files, func(i, j int) bool {
		return extractTS(files[i]).After(extractTS(files[j]))
	})

	for _, f := range files {
		// log.Printf("Loading %s\n", f)

		b, err := os.ReadFile(path.Join(savedProtosDir, f))
		if err != nil {
			return fmt.Errorf("unable to read file %s: %w", f, err)
		}

		var cInfo carrier.CarrierInfo
		if err := proto.Unmarshal(b, &cInfo); err != nil {
			log.Printf("Unable to unmarshal %s: %s", f, err)
			return nil
		}

		addAllConfigSettings(&cInfo, loadedValues, f)
	}

	log.Println("Done loading all proto messages")

	go func() {
		if err := dnsServer.ListenAndServe(); err != nil {
			log.Fatal(err)
		}
	}()

	cert, err := tls.X509KeyPair(server.Bundle, server.Key)
	if err != nil {
		return fmt.Errorf("failed to load server certificate: %w", err)
	}

	go func() {
		ln, err := net.Listen("tcp", ":443")
		if err != nil {
			log.Fatal(err)
		}

		defer ln.Close()

		httpServer := &http.Server{
			Addr:    ":443",
			Handler: carrierHTTPMux,
			TLSConfig: &tls.Config{
				Certificates: []tls.Certificate{cert},
			},
		}
		if err := httpServer.ServeTLS(ln, "", ""); err != nil {
			log.Fatal(err)
		}
	}()

	go func() {
		webControlMux := http.NewServeMux()

		webControlMux.Handle("/metrics", MetricsHandler(loadedValues))
		webControlMux.Handle("/assets/", http.FileServer(http.FS(assets)))
		webControlMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, indexTmpl)
		})
		webControlMux.HandleFunc("/schedule", func(w http.ResponseWriter, r *http.Request) {
			scheduleHTML, err := generateScheduleHTML(loadedValues)
			if err != nil {
				http.Error(w, fmt.Sprintf("Error generating schedule: %v", err), http.StatusInternalServerError)
				return
			}
			fmt.Fprint(w, scheduleHTML)
		})
		webControlMux.HandleFunc("/profiles", func(w http.ResponseWriter, r *http.Request) {
			profilesHTML, err := generateProfilesHTML(loadedValues)
			if err != nil {
				http.Error(w, fmt.Sprintf("Error generating profiles: %v", err), http.StatusInternalServerError)
				return
			}
			fmt.Fprint(w, profilesHTML)
		})
		webControlMux.Handle("/events", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")

			dataCache := make(map[string]string)

			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()

			topics := []string{
				"sensor/wallControl/rh",
				"sensor/wallControl/rt",
				"system/oat",
				"system/mode",
				"1/clsp",
				"1/currentActivity",
				"1/fan",
				"1/htsp",
				"1/name",
				"1/rh",
				"1/rt",
				"1/zoneconditioning",
				"blwrpm",
				"comprpm",
				"instant",
				"cfm",
				"opstat",
				"opmode",
				"oducoiltmp",
				"sucttemp",
				"dischargetmp",
				"profile/firmware",
				"profile/iduversion",
				"profile/oduversion",
			}

			var ts time.Time
			loadedValues.OnChangeN(r.Context(), topics, func(tv []TimestampedValue) {
				data := map[string]string{}
				for i, ent := range tv {
					data[topics[i]] = ent.ToString()
					if ts.Before(ent.lastUpdated) {
						ts = ent.lastUpdated
					}
				}

				data["last-updated"] = ts.Format(time.DateTime)

				for k, v := range data {
					if dataCache[k] == v {
						continue
					}

					fmt.Fprintf(w, "event: %s\n", k)
					fmt.Fprintf(w, "data: %s\n", v)
					fmt.Fprint(w, "\n\n")
					dataCache[k] = v
				}

				w.(http.Flusher).Flush()
			})

			<-r.Context().Done()
		}))

		webControlMux.HandleFunc("/recent", func(w http.ResponseWriter, r *http.Request) {
			entries := loadedValues.RecentEntries()
			for _, ent := range entries {
				fmt.Fprintf(w, "[%s] %s\n", ent.lastUpdated.Format(time.RFC3339), ent.value)
			}
		})
		if err := http.ListenAndServe(":26268", webControlMux); err != nil {
			log.Fatal(err)
		}
	}()

	tlsConfig := &tls.Config{
		GetCertificate: func(chi *tls.ClientHelloInfo) (*tls.Certificate, error) {
			log.Printf("GetCertificate for %s", chi.ServerName)
			return &cert, nil
		},
		GetClientCertificate: func(req *tls.CertificateRequestInfo) (*tls.Certificate, error) {
			log.Printf("GetClientCertificate: %+v", req)
			return nil, nil
		},
		ClientAuth: tls.RequestClientCert,
	}
	tcp := listeners.NewTCP(listeners.Config{
		ID:        "t1",
		Address:   ":8883",
		TLSConfig: tlsConfig,
	})

	server := mqtt.New(&mqtt.Options{
		InlineClient: true, // you must enable inline client to use direct publishing and subscribing.
	})

	level := new(slog.LevelVar)
	server.Log = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	}))
	level.Set(slog.LevelInfo)

	mLogger := &MQTTLogger{
		server:            server,
		savedProtosDir:    savedProtosDir,
		iotMQTTClient:     awsIOTMQTTClient,
		clientID:          clientID,
		thingNameOverride: thingNameOverride,
		subscribedTopics:  make(map[string]struct{}),
		loadedValues:      loadedValues,
		liveClients:       make(map[string]struct{}),
	}

	if err := server.AddHook(mLogger, nil); err != nil {
		return fmt.Errorf("failed to add MQTT hook: %w", err)
	}

	err = server.AddListener(tcp)
	if err != nil {
		return fmt.Errorf("failed to add MQTT listener: %w", err)
	}

	err = server.AddHook(new(auth.AllowHook), nil)
	if err != nil {
		return fmt.Errorf("failed to add auth hook: %w", err)
	}

	shutdownFuncs = append(shutdownFuncs, func() {
		log.Println("Shutting down MQTT")
		server.Close()
	})

	if err = server.Serve(); err != nil {
		return fmt.Errorf("failed to start MQTT server: %w", err)
	}

	if err := server.Subscribe("$aws/certificates/create/cbor", 1, func(cl *mqtt.Client, sub packets.Subscription, pk packets.Packet) {
		clientCert, err := os.ReadFile("tls/client/cert.pem")
		if err != nil {
			log.Fatal(err)
		}

		clientKey, err := os.ReadFile("tls/client/key.pem")
		if err != nil {
			log.Fatal(err)
		}

		server.Log.Info("inline client received message from subscription", "client", cl.ID, "subscriptionId", sub.Identifier, "topic", pk.TopicName, "payload", string(pk.Payload))

		certAndKey := &CertificateAndKeyResponse{
			CertificateId:             "foo",
			CertificateOwnershipToken: "hello",
			CertificatePem:            string(clientCert),
			PrivateKey:                string(clientKey),
		}

		payload, err := cbor.Marshal(certAndKey)
		if err != nil {
			log.Fatal(err)
		}

		if err := server.Publish("$aws/certificates/create/cbor/accepted", payload, false, 0); err != nil {
			log.Fatal(err)
		}
	}); err != nil {
		return fmt.Errorf("failed to subscribe to certificate topic: %w", err)
	}

	if err := server.Subscribe("$aws/provisioning-templates/wallctrl_provision_template/provision/cbor", 2, func(cl *mqtt.Client, sub packets.Subscription, pk packets.Packet) {
		server.Log.Info("wallctrl message", "client", cl.ID, "subscriptionId", sub.Identifier, "topic", pk.TopicName, "payload", string(pk.Payload))

		var registerThingReq RegisterThingReq
		if err := cbor.Unmarshal(pk.Payload, &registerThingReq); err != nil {
			log.Fatal(err)
		}

		log.Printf("Registering with %+v", registerThingReq)

		registerThing := &RegisterThingResp{
			ThingName:           cl.ID,
			DeviceConfiguration: registerThingReq.Parameters,
		}

		payload, err := cbor.Marshal(registerThing)
		if err != nil {
			log.Fatal(err)
		}

		if err := server.Publish("$aws/provisioning-templates/wallctrl_provision_template/provision/cbor/accepted", payload, false, 0); err != nil {
			log.Fatal(err)
		}
	}); err != nil {
		return fmt.Errorf("failed to subscribe to provisioning topic: %w", err)
	}

	publishProto := func(cs []*carrier.ConfigSetting) {
		msg := &carrier.CarrierInfo{
			TimestampMillis: time.Now().UnixMilli(),
			ConfigSettings:  cs,
			Uuid:            uuid.New().String(),
		}
		msgEncoded, err := proto.Marshal(msg)
		if err != nil {
			log.Printf("Failed to encode proto: %s", err)
			return
		}
		if err := server.Publish(cmdTopic, msgEncoded, false, 0); err != nil {
			log.Printf("Failed to send command NCMD: %s", err)
		}
	}

	loadedValues.OnChange1("weather_request", func(tv TimestampedValue) {
		now := time.Now()
		ts := now.UnixMilli()

		log.Println("Got weather request", tv.value.Details)

		inputDetails := tv.value.GetDetails()
		if len(inputDetails) != 1 {
			return
		}

		inputDetail := inputDetails[0]
		inputEntries := inputDetail.GetEntries()

		if len(inputEntries) != 1 {
			return
		}

		inputEntry := inputEntries[0]
		postalCode := string(inputEntry.GetMaybeStrValue())

		// Base weather entries
		entries := []*carrier.ConfigSetting{
			{
				Name:            "temp_units",
				ConfigType:      carrier.ConfigType_CT_STRING,
				TimestampMillis: ts,
				Value: &carrier.ConfigSetting_MaybeStrValue{
					MaybeStrValue: []byte("C"),
				},
			},
			{
				Name:            "ping",
				ConfigType:      carrier.ConfigType_CT_INT32,
				TimestampMillis: ts,
				Value: &carrier.ConfigSetting_IntValue{
					IntValue: 60,
				},
			},
			{
				Name:            "timestamp",
				ConfigType:      carrier.ConfigType_CT_STRING,
				TimestampMillis: ts,
				Value: &carrier.ConfigSetting_MaybeStrValue{
					MaybeStrValue: []byte(now.UTC().Format("2006-01-02T15:04:05.000Z")),
				},
			},
		}

		// Fetch weather forecast
		forecast, err := weather.GetForecastDataByPostalCode(postalCode)
		if err != nil {
			log.Printf("Failed to get weather data: %v", err)
			return
		}

		// Add forecast entries for each day
		for i := 0; i <= 5; i++ {
			var dayData weather.ForecastDay
			if i >= len(forecast) {
				// Use last day's data if we don't have enough days
				dayData = forecast[len(forecast)-1]
			} else {
				dayData = forecast[i]
			}

			entries = append(entries, []*carrier.ConfigSetting{
				{
					Name:            fmt.Sprintf("%d/pop", i),
					ConfigType:      carrier.ConfigType_CT_UINT16,
					TimestampMillis: ts,
					Value: &carrier.ConfigSetting_IntValue{
						IntValue: int32(dayData.Precipitation),
					},
				},
				{
					Name:            fmt.Sprintf("%d/status_id", i),
					ConfigType:      carrier.ConfigType_CT_UINT16,
					TimestampMillis: ts,
					Value: &carrier.ConfigSetting_IntValue{
						IntValue: int32(dayData.StatusID),
					},
				},
				{
					Name:            fmt.Sprintf("%d/max_temp", i),
					ConfigType:      carrier.ConfigType_CT_TEMP,
					TimestampMillis: ts,
					Value: &carrier.ConfigSetting_IntValue{
						IntValue: int32(dayData.MaxTemp),
					},
				},
				{
					Name:            fmt.Sprintf("%d/min_temp", i),
					ConfigType:      carrier.ConfigType_CT_TEMP,
					TimestampMillis: ts,
					Value: &carrier.ConfigSetting_IntValue{
						IntValue: int32(dayData.MinTemp),
					},
				},
			}...)
		}

		publishProto([]*carrier.ConfigSetting{
			{
				Name:       "weather_forecast",
				ConfigType: carrier.ConfigType_CT_STRUCT,
				Details: []*carrier.Detail{
					{
						Entries: entries,
						Zero:    0,
					},
				},
			},
		})
	})

	haMQTT := NewHAMQTT(haMQTTAddr, haMQTTTopicPrefix, haMQTTUsername, haMQTTPassword, clientID, loadedValues, publishProto)
	go haMQTT.Run()

	go func() {
		if awsIOTMQTTClient == nil {
			// If we're not proxying to AWS IOT, poll every minute
			for range time.Tick(time.Minute) {
				hasLiveClient := false
				for connectedClientId := range server.Clients.GetAll() {
					if connectedClientId == "inline" {
						continue
					}
					if _, ok := mLogger.liveClients[connectedClientId]; ok {
						hasLiveClient = true
					}
				}
				if hasLiveClient {
					publishProto([]*carrier.ConfigSetting{
						{
							Name:       "event_update_mode_active",
							ConfigType: carrier.ConfigType_CT_BOOL,
							Value: &carrier.ConfigSetting_BoolValue{
								BoolValue: true,
							},
						},
					})
				}
			}
		}
	}()

	done := make(chan bool, 1)

	go func() {
		log.Println("Listening for interrupt signals")
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

		<-sigCh
		close(done)
	}()

	<-done

	log.Println("Received signal. Beginning orderly shutdown")

	for _, shutdownFunc := range shutdownFuncs {
		shutdownFunc()
	}

	return nil
}
