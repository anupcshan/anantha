package cmd

import (
	"crypto/tls"
	"embed"
	"errors"
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
	"regexp"
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

func GetExternalIP(externalIPOverride string) (net.IP, error) {
	if externalIPOverride != "" {
		parsedIP := net.ParseIP(externalIPOverride)
		if parsedIP != nil {
			return parsedIP, nil
		} else {
			return nil, errors.New("failed to parse manually specified external IP")
		}
	} else {
		conn, err := net.Dial("udp", "1.1.1.1:80")
		if err != nil {
			return nil, err
		}
		defer conn.Close()

		localAddr := conn.LocalAddr().(*net.UDPAddr)

		return localAddr.IP, nil
	}
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
	serveCmd.Flags().String("external-ip-override", "", "Override auto-detected external IP")
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
	externalIPOverride, _ := cmd.Flags().GetString("external-ip-override")
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

	externalIP, err := GetExternalIP(externalIPOverride)
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
			indexHTML, err := RenderIndex()
			if err != nil {
				http.Error(w, fmt.Sprintf("Error rendering index: %v", err), http.StatusInternalServerError)
				return
			}
			fmt.Fprint(w, indexHTML)
		})
		webControlMux.HandleFunc("/schedule", func(w http.ResponseWriter, r *http.Request) {
			scheduleHTML, err := RenderSchedule(loadedValues)
			if err != nil {
				http.Error(w, fmt.Sprintf("Error generating schedule: %v", err), http.StatusInternalServerError)
				return
			}
			fmt.Fprint(w, scheduleHTML)
		})
		webControlMux.HandleFunc("/profiles", func(w http.ResponseWriter, r *http.Request) {
			profilesHTML, err := RenderProfiles(loadedValues)
			if err != nil {
				http.Error(w, fmt.Sprintf("Error generating profiles: %v", err), http.StatusInternalServerError)
				return
			}
			fmt.Fprint(w, profilesHTML)
		})
		webControlMux.HandleFunc("/vacation-banner", func(w http.ResponseWriter, r *http.Request) {
			snapshot := loadedValues.Snapshot()

			vacEnabled, hasVacEnabled := snapshot["system/vacat/vacat"]
			vacStart, hasVacStart := snapshot["system/vacat/vacstart"]
			vacEnd, hasVacEnd := snapshot["system/vacat/vacend"]

			// Only show banner if vacation is enabled and we have start/end times
			if !hasVacEnabled || vacEnabled.ToString() != "true" || !hasVacStart || !hasVacEnd {
				return
			}

			now := time.Now()
			startTime, err := time.Parse(time.RFC3339, vacStart.ToString())
			if err != nil {
				return
			}
			endTime, err := time.Parse(time.RFC3339, vacEnd.ToString())
			if err != nil {
				return
			}

			var bannerHTML string
			if now.Before(startTime) {
				// Vacation is scheduled but hasn't started yet
				bannerHTML = fmt.Sprintf(`<div class="vacation-banner">
					<div class="vacation-icon">🏖️</div>
					<span>Vacation Mode starts %s</span>
				</div>`, startTime.Local().Format("Jan 2, 3:04 PM"))
			} else if now.After(startTime) && now.Before(endTime) {
				// Currently in vacation period
				bannerHTML = fmt.Sprintf(`<div class="vacation-banner">
					<div class="vacation-icon">🏖️</div>
					<span>Vacation Mode Active until %s</span>
				</div>`, endTime.Local().Format("Jan 2, 3:04 PM"))
			}
			// If vacation has ended, don't show anything

			fmt.Fprint(w, bannerHTML)
		})
		webControlMux.Handle("/events", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")

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
				"system/vacat/vacat",
				"system/vacat/vacstart",
				"system/vacat/vacend",
				// Energy usage
				"/usage/day1/hpheat",
				"/usage/day2/hpheat",
				"/usage/month1/hpheat",
				"/usage/month2/hpheat",
				"/usage/year1/hpheat",
				"/usage/year2/hpheat",
				"/usage/day1/cooling",
				"/usage/day2/cooling",
				"/usage/month1/cooling",
				"/usage/month2/cooling",
				"/usage/year1/cooling",
				"/usage/year2/cooling",
				"/usage/day1/fan",
				"/usage/day2/fan",
				"/usage/month1/fan",
				"/usage/month2/fan",
				"/usage/year1/fan",
				"/usage/year2/fan",
			}

			currentValues := make(map[string]TimestampedValue)
			dataCache := make(map[string]string)
			var ts time.Time

			sendEvent := func(event, data string) {
				if dataCache[event] == data {
					return
				}
				dataCache[event] = data
				fmt.Fprintf(w, "event: %s\n", event)
				fmt.Fprintf(w, "data: %s\n", data)
				fmt.Fprint(w, "\n\n")
				w.(http.Flusher).Flush()
			}

			sendUsageTotal := func(period string) {
				getUsageValue := func(topic string) int32 {
					if ent, ok := currentValues[topic]; ok && ent.value != nil {
						if ent.value.ConfigType == carrier.ConfigType_CT_INT64 {
							return ent.value.GetAnotherIntValue()
						}
						return ent.value.GetIntValue()
					}
					return 0
				}
				total := getUsageValue("/usage/"+period+"/hpheat") +
					getUsageValue("/usage/"+period+"/cooling") +
					getUsageValue("/usage/"+period+"/fan")
				sendEvent("usage-total-"+period, fmt.Sprintf("%d kWh", total))

				// Send label for this period (dataCache handles deduplication)
				now := time.Now()
				switch period {
				case "day1":
					sendEvent("usage-label-day1", now.AddDate(0, 0, -1).Format("Mon, Jan 2 2006"))
				case "day2":
					sendEvent("usage-label-day2", now.AddDate(0, 0, -2).Format("Mon, Jan 2 2006"))
				case "month1":
					sendEvent("usage-label-month1", now.AddDate(0, -1, 0).Format("Jan 2006"))
				case "month2":
					sendEvent("usage-label-month2", now.AddDate(0, -2, 0).Format("Jan 2006"))
				case "year1":
					sendEvent("usage-label-year1", now.Format("2006"))
				case "year2":
					sendEvent("usage-label-year2", now.AddDate(-1, 0, 0).Format("2006"))
				}
			}

			processUpdate := func(tv TimestampedValue) {
				currentValues[tv.value.Name] = tv
				if ts.Before(tv.lastUpdated) {
					ts = tv.lastUpdated
				}

				sendEvent(tv.value.Name, tv.ToString())
				sendEvent("last-updated", ts.Format(time.DateTime))

				// If it's a usage topic, recompute the relevant total
				if strings.HasPrefix(tv.value.Name, "/usage/") {
					parts := strings.Split(tv.value.Name, "/")
					if len(parts) >= 3 {
						sendUsageTotal(parts[2]) // e.g., "day1", "month1", etc.
					}
				}
			}

			// Send initial values
			for _, topic := range topics {
				if v := loadedValues.Get(topic); v.value != nil {
					processUpdate(v)
				}
			}

			// Subscribe and process updates
			subCh := loadedValues.Subscribe(topics)
			for {
				select {
				case <-r.Context().Done():
					return
				case tv := <-subCh:
					processUpdate(tv)
				}
			}
		}))

		webControlMux.HandleFunc("/recent", func(w http.ResponseWriter, r *http.Request) {
			entries := loadedValues.RecentEntries()
			for _, ent := range entries {
				fmt.Fprintf(w, "[%s] %s\n", ent.lastUpdated.Format(time.RFC3339), ent.value)
			}
		})

		webControlMux.HandleFunc("/mqtt-log", func(w http.ResponseWriter, r *http.Request) {
			mqttLogHTML, err := RenderMQTTLog()
			if err != nil {
				http.Error(w, fmt.Sprintf("Error rendering mqtt-log: %v", err), http.StatusInternalServerError)
				return
			}
			fmt.Fprint(w, mqttLogHTML)
		})

		webControlMux.HandleFunc("/mqtt-log-stream", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			w.Header().Set("Access-Control-Allow-Origin", "*")

			log.Printf("New MQTT log stream connection from %s", r.RemoteAddr)

			// Send initial message to clear loading state
			fmt.Fprintf(w, "data: <div class='log-entry'>[%s] Connected to MQTT log stream</div><div id='loading-msg' hx-swap-oob='delete'></div>\n\n", time.Now().Format(time.RFC3339))
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}

			// Use regex to catch all MQTT updates
			re := regexp.MustCompile(".*")
			updateCh := loadedValues.RegexSubscribe(re)

			ctx := r.Context()

			for {
				select {
				case <-ctx.Done():
					log.Printf("MQTT log stream connection closed for %s", r.RemoteAddr)
					return
				case update := <-updateCh:
					fmt.Fprintf(w, "data: <div class='log-entry'>[%s] %s</div>\n\n", update.lastUpdated.Format(time.RFC3339), update.value)
					if flusher, ok := w.(http.Flusher); ok {
						flusher.Flush()
					}
				}
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
