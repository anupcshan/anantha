package cmd

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"sync"
	"time"
	"unicode/utf8"

	carrier "github.com/anupcshan/anantha/pb"
	"google.golang.org/protobuf/encoding/prototext"
)

type TimestampedValue struct {
	value       *carrier.ConfigSetting
	lastUpdated time.Time
}

func (t TimestampedValue) ToString() string {
	if t.value == nil {
		return "unknown"
	}

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

type RegexSub struct {
	ch chan TimestampedValue
	re *regexp.Regexp
}

type LoadedValues struct {
	values map[string]TimestampedValue
	lock   sync.Mutex

	globalLastUpdated time.Time

	subscriptions       map[string][]chan TimestampedValue
	regexSubscriptions  []RegexSub
	globalSubscriptions []chan time.Time
}

func (l *LoadedValues) Update(k string, v *carrier.ConfigSetting, ts time.Time) bool {
	l.lock.Lock()
	defer l.lock.Unlock()

	if ts.After(l.globalLastUpdated) {
		l.globalLastUpdated = ts

		for _, sub := range l.globalSubscriptions {
			select {
			case sub <- ts:
			default: // Don't block
			}
		}
	}

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

	for _, sub := range l.regexSubscriptions {
		if sub.re.MatchString(k) {
			select {
			case sub.ch <- l.values[k]:
			default: // Don't block
			}
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

func (l *LoadedValues) GlobalSubscribe() <-chan time.Time {
	ch := make(chan time.Time, 100) // Large enough to not cause dropping

	l.lock.Lock()
	defer l.lock.Unlock()

	l.globalSubscriptions = append(l.globalSubscriptions, ch)

	return ch
}

func (l *LoadedValues) RegexSubscribe(re *regexp.Regexp) <-chan TimestampedValue {
	ch := make(chan TimestampedValue, 100) // Large enough to not cause dropping

	l.lock.Lock()
	defer l.lock.Unlock()

	l.regexSubscriptions = append(l.regexSubscriptions, RegexSub{
		ch: ch,
		re: re,
	})

	return ch
}

func (l *LoadedValues) OnChangeRegex(re *regexp.Regexp, callback func(TimestampedValue)) {
	subCh := l.RegexSubscribe(re)

	l.lock.Lock()
	defer l.lock.Unlock()

	for k, v := range l.values {
		if re.MatchString(k) {
			callback(v)
		}
	}

	go func() {
		for ch := range subCh {
			callback(ch)
		}
	}()
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

func (l *LoadedValues) OnChangeN(ctx context.Context, topics []string, callback func([]TimestampedValue)) {
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
		for {
			select {
			case <-ctx.Done():
				return
			case ch := <-subCh:
				recentValues[ch.value.Name] = ch
				maybeCallback()
			}
		}
	}()
}

func (l *LoadedValues) OnChange1(topic string, callback func(TimestampedValue)) {
	l.OnChangeN(context.Background(), []string{topic}, func(tv []TimestampedValue) {
		callback(tv[0])
	})
}

func (l *LoadedValues) OnChange2(topic1, topic2 string, callback func(val1, val2 TimestampedValue)) {
	l.OnChangeN(context.Background(), []string{topic1, topic2}, func(tv []TimestampedValue) {
		callback(tv[0], tv[1])
	})
}

func (l *LoadedValues) OnAnyChange(callback func(time.Time)) {
	subCh := l.GlobalSubscribe()

	l.lock.Lock()
	if !l.globalLastUpdated.IsZero() {
		callback(l.globalLastUpdated)
	}
	l.lock.Unlock()

	go func() {
		for ch := range subCh {
			callback(ch)
		}
	}()
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
