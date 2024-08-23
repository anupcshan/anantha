package main

import (
	"crypto/tls"
	"embed"
	_ "embed"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	carrier "github.com/anupcshan/anantha/pb"
	"github.com/fxamacker/cbor/v2"
	"github.com/google/uuid"
	"github.com/miekg/dns"
	"google.golang.org/protobuf/encoding/prototext"
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

const (
	indexTmpl = `
<!DOCTYPE html>
<html>
<head>
	<script src="/assets/htmx.org@1.9.12/dist/htmx.min.js"></script>
	<script src="/assets/htmx.org@1.9.12/dist/ext/sse.js"></script>
	<style>
	table, th, td {
		border: 1px solid black;
		border-collapse: collapse;
		padding: 5px;
		text-align: center;
	}
	</style>
</head>
<body>
	<div hx-ext="sse" sse-connect="/events">
		<div>Last updated: <span sse-swap="last-updated">Never</span></div><br/>
		<table>
			<tr>
				<th>Outside Temp</th>
				<th>Mode</th>
			</tr>
			<tr>
				<td sse-swap="system/oat">Pending</td>
				<td sse-swap="system/mode">Pending</td>
			</tr>
		</table>

		<br/>

		<div><b sse-swap="1/name">Zone Name Pending</b></div><br/>

		<table>
			<tr>
				<th>Temp</th>
				<th>Humidity</th>
				<th>Current Activity</th>
				<th>Conditioning</th>
				<th>Heat Setpoint</th>
				<th>Cool Setpoint</th>
				<th>Fan</th>
			</tr>
			<tr>
				<td sse-swap="1/rt">Pending</td>
				<td sse-swap="1/rh">Pending</td>
				<td sse-swap="1/currentActivity">Pending</td>
				<td sse-swap="1/zoneconditioning">Pending</td>
				<td sse-swap="1/htsp">Pending</td>
				<td sse-swap="1/clsp">Pending</td>
				<td sse-swap="1/fan">Pending</td>
			</tr>
		</table>

		<br/>

		<div><b>Details</b></div><br/>

		<table>
			<tr>
				<th>Blower RPM</th>
				<th>Compressor RPM</th>
				<th>Instant Power</th>
				<th>Airflow CFM</th>
				<th>Oper Status</th>
				<th>Oper Mode</th>
				<th>Coil Temp</th>
				<th>Suction Temp</th>
				<th>Discharge Temp</th>
			</tr>
			<tr>
				<td sse-swap="blwrpm">Pending</td>
				<td sse-swap="comprpm">Pending</td>
				<td sse-swap="instant">Pending</td>
				<td sse-swap="cfm">Pending</td>
				<td sse-swap="opstat">Pending</td>
				<td sse-swap="opmode">Pending</td>
				<td sse-swap="oducoiltmp">Pending</td>
				<td sse-swap="sucttemp">Pending</td>
				<td sse-swap="dischargetmp">Pending</td>
			</tr>
		</table>
	</div>
</body>
</html>
`
)

type MQTTLogger struct {
	mqtt.HookBase

	server            *mqtt.Server
	savedReqsDir      string
	iotMQTTClient     mqtt_paho.Client
	clientID          string
	thingNameOverride string

	subscribedTopics     map[string]struct{}
	subscribedTopicsLock sync.Mutex

	forwardMessageLock sync.Mutex

	loadedValues *LoadedValues
}

// ID returns the ID of the hook.
func (m *MQTTLogger) ID() string {
	return "logger"
}

// Provides indicates that this hook provides all methods.
func (m *MQTTLogger) Provides(b byte) bool {
	return true
}

func addAllConfigSettings(ct *carrier.CarrierInfo, loadedValues *LoadedValues) int {
	var updated int
	for _, setting := range ct.ConfigSettings {
		if loadedValues.Update(setting.Name, setting, time.UnixMilli(ct.TimestampMillis)) {
			updated++
		}
	}

	return updated
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
		if err := os.WriteFile(
			fmt.Sprintf(
				"%s/%s-%s.pb",
				m.savedReqsDir,
				strings.ReplaceAll(string(pk.TopicName), "/", "_"),
				time.Now().Format(time.RFC3339Nano),
			),
			pk.Payload,
			0644,
		); err != nil {
			log.Printf("Error writing file: %s", err)
		}
		var ct carrier.CarrierInfo
		if err := proto.Unmarshal(pk.Payload, &ct); err != nil {
			log.Printf("Failed to unmarshal: %s", err)
		}

		addAllConfigSettings(&ct, m.loadedValues)

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
							m.savedReqsDir,
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

type TimestampedValue struct {
	value       *carrier.ConfigSetting
	lastUpdated time.Time
}

func (t TimestampedValue) ToString() string {
	// For known keys, include units and format it nicely
	switch t.value.Name {
	case "sensor/wallControl/rh":
		return fmt.Sprintf("%d %%", t.value.GetAnotherIntValue())
	case "1/rh":
		return fmt.Sprintf("%.1f %%", t.value.GetFloatValue())
	case "sensor/wallControl/rt", "1/htsp", "1/clsp", "1/rt":
		return fmt.Sprintf("%.1f F", t.value.GetFloatValue())
	case "system/oat", "oducoiltmp", "dischargetmp", "sucttemp":
		return fmt.Sprintf("%d F", t.value.GetIntValue())
	case "blwrpm", "comprpm":
		return fmt.Sprintf("%d RPM", t.value.GetIntValue())
	case "instant":
		return fmt.Sprintf("%d W", t.value.GetIntValue())
	case "cfm":
		return fmt.Sprintf("%d CFM", t.value.GetIntValue())
	}

	switch t.value.ConfigType {
	case carrier.ConfigType_CT_BOOL:
		return fmt.Sprintf("%t", t.value.GetBoolValue())
	case carrier.ConfigType_CT_STRING:
		if utf8.Valid(t.value.GetMaybeStrValue()) {
			return string(t.value.GetMaybeStrValue())
		}
		return fmt.Sprintf("hex(%x)", t.value.GetMaybeStrValue())
	case carrier.ConfigType_CT_FLOAT:
		return fmt.Sprintf("%f", t.value.GetFloatValue())
	case carrier.ConfigType_CT_INT16, carrier.ConfigType_CT_INT, carrier.ConfigType_CT_INT32, carrier.ConfigType_CT_UINT16:
		return fmt.Sprintf("%d", t.value.GetIntValue())
	case carrier.ConfigType_CT_INT64:
		return fmt.Sprintf("%d", t.value.GetAnotherIntValue())
	}

	// Fallback
	return prototext.Format(t.value)
}

type LoadedValues struct {
	values map[string]TimestampedValue
	lock   sync.Mutex

	subscriptions map[string][]chan TimestampedValue
}

func (l *LoadedValues) Update(k string, v *carrier.ConfigSetting, ts time.Time) bool {
	l.lock.Lock()
	defer l.lock.Unlock()

	if existing, ok := l.values[k]; ok {
		if existing.lastUpdated.After(ts) {
			return false
		}
	}

	l.values[k] = TimestampedValue{
		value:       v,
		lastUpdated: ts,
	}

	for _, sub := range l.subscriptions[k] {
		select {
		case sub <- l.values[k]:
		default: // Don't block
		}
	}

	return true
}

func (l *LoadedValues) Get(key string) TimestampedValue {
	l.lock.Lock()
	defer l.lock.Unlock()

	return l.values[key]
}

func (l *LoadedValues) Snapshot() map[string]TimestampedValue {
	l.lock.Lock()
	defer l.lock.Unlock()

	result := make(map[string]TimestampedValue, len(l.values))

	for k, v := range l.values {
		result[k] = v
	}

	return result
}

func (l *LoadedValues) Subscribe(topics []string) <-chan TimestampedValue {
	ch := make(chan TimestampedValue, 100) // Large enough to not cause dropping

	l.lock.Lock()
	defer l.lock.Unlock()

	for _, topic := range topics {
		l.subscriptions[topic] = append(l.subscriptions[topic], ch)
	}

	return ch
}

func (l *LoadedValues) OnChangeN(topics []string, callback func([]TimestampedValue)) {
	recentValues := map[string]TimestampedValue{}

	subCh := l.Subscribe(topics)

	l.lock.Lock()
	for _, topic := range topics {
		if val, ok := l.values[topic]; ok {
			recentValues[topic] = val
		}
	}
	l.lock.Unlock()

	maybeCallback := func() {
		if len(recentValues) == len(topics) {
			var args []TimestampedValue
			for _, topic := range topics {
				args = append(args, recentValues[topic])
			}
			callback(args)
		}
	}

	maybeCallback()

	go func() {
		for ch := range subCh {
			recentValues[ch.value.Name] = ch
			maybeCallback()
		}
	}()
}

func (l *LoadedValues) OnChange1(topic string, callback func(TimestampedValue)) {
	l.OnChangeN([]string{topic}, func(tv []TimestampedValue) {
		callback(tv[0])
	})
}

func (l *LoadedValues) OnChange2(topic1, topic2 string, callback func(val1, val2 TimestampedValue)) {
	l.OnChangeN([]string{topic1, topic2}, func(tv []TimestampedValue) {
		callback(tv[0], tv[1])
	})
}

func (l *LoadedValues) RecentEntries() []TimestampedValue {
	l.lock.Lock()
	defer l.lock.Unlock()

	result := make([]TimestampedValue, 0, len(l.values))

	for _, v := range l.values {
		result = append(result, v)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].lastUpdated.Equal(result[j].lastUpdated) {
			return result[i].value.Name < result[j].value.Name
		}

		return result[i].lastUpdated.After(result[j].lastUpdated)
	})

	return result
}

func NewLoadedValues() *LoadedValues {
	return &LoadedValues{
		values:        map[string]TimestampedValue{},
		subscriptions: map[string][]chan TimestampedValue{},
	}
}

func main() {
	ntpAddrStr := flag.String("ntp-addr", "", "NTP IPv4 Address")
	haMQTTAddr := flag.String("ha-mqtt-addr", "", "Home Assistant MQTT Host")
	haMQTTTopicPrefix := flag.String("ha-mqtt-topic-prefix", "", "Home Assistant MQTT Topic Prefix")
	savedReqsDir := flag.String("reqs-dir", "dump", "Directory where request protos are stored")
	clientID := flag.String("client-id", "hello", "MQTT Client ID")
	thingNameOverride := flag.String("thing-name-override", "", "Thingname override - you should never need to set this")
	proxyToAWSIOT := flag.Bool("proxy", false, "Proxy requests to AWS IOT - requires a valid client certificate for now")

	flag.Parse()

	log.SetFlags(log.Lmicroseconds | log.Lshortfile)

	var shutdownFuncs []func()
	var shutdownFuncLock sync.Mutex

	loadedValues := NewLoadedValues()

	cmdTopic := fmt.Sprintf("spBv1.0/WallCtrl/NCMD/%s", *clientID)
	if *thingNameOverride != "" {
		cmdTopic = fmt.Sprintf("spBv1.0/WallCtrl/NCMD/%s", *thingNameOverride)
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
		log.Fatal(err)
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
	if len(*ntpAddrStr) > 0 {
		ntpAddr := net.ParseIP(*ntpAddrStr)
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

	clientCert, err := tls.LoadX509KeyPair("tls/client/cert.pem", "tls/client/key.pem")
	if err != nil {
		log.Fatal(err)
	}

	var awsIOTMQTTClient mqtt_paho.Client
	if *proxyToAWSIOT {
		awsIOTMQTTClient = mqtt_paho.NewClient(
			mqtt_paho.NewClientOptions().
				AddBroker("mqtts://mqtt.res.carrier.io:443").
				SetClientID(*clientID).
				SetTLSConfig(&tls.Config{
					Certificates: []tls.Certificate{clientCert},
					NextProtos:   []string{"x-amzn-mqtt-ca"},
				}),
		)

		log.Printf("Connecting to mqtt.res.carrier.io")
		if token := awsIOTMQTTClient.Connect(); token.Wait() && token.Error() != nil {
			log.Fatalf("Error connecting to MQTT: %s", token.Error())
		}
		log.Printf("Connected to mqtt.res.carrier.io")
	}

	go func() {
		dirents, err := os.ReadDir(*savedReqsDir)
		if err != nil {
			log.Fatalf("Failed to list directory: %s", err)
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
			log.Printf("Loading %s\n", f)

			b, err := os.ReadFile(path.Join(*savedReqsDir, f))
			if err != nil {
				log.Fatalf("Unable to read file %s: %s", f, err)
			}

			var cInfo carrier.CarrierInfo
			if err := proto.Unmarshal(b, &cInfo); err != nil {
				log.Printf("Unable to unmarshal %s: %s", f, err)
				return
			}

			if updated := addAllConfigSettings(&cInfo, loadedValues); updated <= 0 {
				log.Printf("File %s had no new records - deleting", f)
				if err := os.Remove(path.Join(*savedReqsDir, f)); err != nil {
					log.Fatalf("Unable to remove file %s: %s", f, err)
				}
			}
		}

		log.Println("Done loading all protos")
	}()

	go func() {
		if err := dnsServer.ListenAndServe(); err != nil {
			log.Fatal(err)
		}
	}()

	go func() {
		if err := http.ListenAndServeTLS(":443", "tls/server/cert-bundle.pem", "tls/server/key.pem", carrierHTTPMux); err != nil {
			log.Fatal(err)
		}
	}()

	go func() {
		webControlMux := http.NewServeMux()

		webControlMux.Handle("/assets/", http.FileServer(http.FS(assets)))
		webControlMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, indexTmpl)
		})
		webControlMux.Handle("/events", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")

			dataCache := make(map[string]string)

			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()

			for {
				entries := loadedValues.Snapshot()
				data := map[string]string{}
				var ts time.Time

				for _, k := range []string{
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
				} {
					ent := entries[k]
					data[k] = ent.ToString()
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

				select {
				case <-r.Context().Done():
					return
				case <-ticker.C:
				}
			}
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

	go func() {
		cert, err := tls.LoadX509KeyPair("tls/server/cert-bundle.pem", "tls/server/key.pem")
		if err != nil {
			log.Fatal(err)
		}
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

		if err := server.AddHook(&MQTTLogger{
			server:            server,
			savedReqsDir:      *savedReqsDir,
			iotMQTTClient:     awsIOTMQTTClient,
			clientID:          *clientID,
			thingNameOverride: *thingNameOverride,
			subscribedTopics:  make(map[string]struct{}),
			loadedValues:      loadedValues,
		}, nil); err != nil {
			log.Fatal(err)
		}

		err = server.AddListener(tcp)
		if err != nil {
			log.Fatal(err)
		}

		err = server.AddHook(new(auth.AllowHook), nil)
		if err != nil {
			log.Fatal(err)
		}

		shutdownFuncLock.Lock()
		shutdownFuncs = append(shutdownFuncs, func() {
			log.Println("Shutting down MQTT")
			server.Close()
		})
		shutdownFuncLock.Unlock()

		if err = server.Serve(); err != nil {
			log.Fatal(err)
		}

		clientCert, err := os.ReadFile("tls/client/cert.pem")
		if err != nil {
			log.Fatal(err)
		}

		clientKey, err := os.ReadFile("tls/client/key.pem")
		if err != nil {
			log.Fatal(err)
		}

		if err := server.Subscribe("$aws/certificates/create/cbor", 1, func(cl *mqtt.Client, sub packets.Subscription, pk packets.Packet) {
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
			log.Fatal(err)
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
			log.Fatal(err)
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
			log.Println("Responding to weather request")
			now := time.Now()
			ts := now.UnixMilli()

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

			for i := 0; i <= 5; i++ {
				entries = append(entries, []*carrier.ConfigSetting{
					{
						Name:            fmt.Sprintf("%d/pop", i),
						ConfigType:      carrier.ConfigType_CT_UINT16,
						TimestampMillis: ts,
						Value: &carrier.ConfigSetting_IntValue{
							IntValue: 0,
						},
					},
					{
						// 1  -> thunderstorms
						// 2  -> sleet
						// 3  -> rain and sleet
						// 4  -> wintry mix
						// 5  -> rain and snow
						// 6  -> snow
						// 7  -> freezing rain
						// 8  -> rain
						// 9  -> blizzard
						// 10 -> fog
						// 11 -> cloudy
						// 12 -> partly cloudy
						// 13 -> mostly cloudy
						// 14 -> clear
						Name:            fmt.Sprintf("%d/status_id", i),
						ConfigType:      carrier.ConfigType_CT_UINT16,
						TimestampMillis: ts,
						Value: &carrier.ConfigSetting_IntValue{
							IntValue: 14,
						},
					},
					{
						Name:            fmt.Sprintf("%d/max_temp", i),
						ConfigType:      carrier.ConfigType_CT_TEMP,
						TimestampMillis: ts,
						Value: &carrier.ConfigSetting_IntValue{
							IntValue: 23,
						},
					},
					{
						Name:            fmt.Sprintf("%d/min_temp", i),
						ConfigType:      carrier.ConfigType_CT_TEMP,
						TimestampMillis: ts,
						Value: &carrier.ConfigSetting_IntValue{
							IntValue: 12,
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

		haMQTT := NewHAMQTT(*haMQTTAddr, *haMQTTTopicPrefix, loadedValues, publishProto)
		go haMQTT.Run()

		if awsIOTMQTTClient == nil {
			// If we're not proxying to AWS IOT, poll every minute
			for range time.Tick(time.Minute) {
				log.Printf("Polling NCMD event_update_mode_active: %+v %+v", server.Clients.Len(), server.Clients.GetAll())
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

	shutdownFuncLock.Lock()
	for _, shutdownFunc := range shutdownFuncs {
		shutdownFunc()
	}
	shutdownFuncLock.Unlock()
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
