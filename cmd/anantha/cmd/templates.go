package cmd

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"time"
)

//go:embed templates/*.html
var templateFS embed.FS

var (
	indexTemplate    *template.Template
	mqttLogTemplate  *template.Template
	profilesTemplate *template.Template
	scheduleTemplate *template.Template
)

func init() {
	var err error

	indexTemplate, err = template.ParseFS(templateFS, "templates/index.html")
	if err != nil {
		panic(fmt.Sprintf("failed to parse index template: %v", err))
	}

	mqttLogTemplate, err = template.ParseFS(templateFS, "templates/mqtt_log.html")
	if err != nil {
		panic(fmt.Sprintf("failed to parse mqtt_log template: %v", err))
	}

	profilesTemplate, err = template.ParseFS(templateFS, "templates/profiles.html")
	if err != nil {
		panic(fmt.Sprintf("failed to parse profiles template: %v", err))
	}

	scheduleTemplate, err = template.ParseFS(templateFS, "templates/schedule.html")
	if err != nil {
		panic(fmt.Sprintf("failed to parse schedule template: %v", err))
	}
}

// RenderIndex renders the index/dashboard template
func RenderIndex() (string, error) {
	var buf bytes.Buffer
	if err := indexTemplate.Execute(&buf, nil); err != nil {
		return "", fmt.Errorf("failed to execute index template: %w", err)
	}
	return buf.String(), nil
}

// RenderMQTTLog renders the MQTT log template
func RenderMQTTLog() (string, error) {
	var buf bytes.Buffer
	if err := mqttLogTemplate.Execute(&buf, nil); err != nil {
		return "", fmt.Errorf("failed to execute mqtt_log template: %w", err)
	}
	return buf.String(), nil
}

// ActivityData holds data for a single activity profile
type ActivityData struct {
	Name          string
	Icon          string
	HasSettings   bool
	HasHtsp       bool
	HtspFormatted string
	HasClsp       bool
	ClspFormatted string
	HasFan        bool
	FanSetting    string
}

// ProfilesData holds data for the profiles template
type ProfilesData struct {
	Activities []ActivityData
}

// RenderProfiles renders the profiles template with data from loadedValues
func RenderProfiles(loadedValues *LoadedValues) (string, error) {
	activities := []string{"home", "away", "sleep", "wake", "manual"}
	snapshot := loadedValues.Snapshot()

	data := ProfilesData{
		Activities: make([]ActivityData, 0, len(activities)),
	}

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

		ad := ActivityData{
			Name:        activity,
			Icon:        icon,
			HasSettings: hasHtsp || hasClsp || hasFan,
			HasHtsp:     hasHtsp,
			HasClsp:     hasClsp,
			HasFan:      hasFan,
		}

		if hasHtsp {
			ad.HtspFormatted = fmt.Sprintf("%.1f°F", htspVal.value.GetFloatValue())
		}
		if hasClsp {
			ad.ClspFormatted = fmt.Sprintf("%.1f°F", clspVal.value.GetFloatValue())
		}
		if hasFan {
			fanSetting := string(fanVal.value.GetMaybeStrValue())
			if fanSetting == "" {
				fanSetting = "auto"
			}
			ad.FanSetting = fanSetting
		}

		data.Activities = append(data.Activities, ad)
	}

	var buf bytes.Buffer
	if err := profilesTemplate.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute profiles template: %w", err)
	}
	return buf.String(), nil
}

// SchedulePeriodData holds data for a single schedule period
type SchedulePeriodData struct {
	StartTime     string
	EndTime       string
	Activity      string
	HeightPercent float64
	DurationStr   string
}

// DayScheduleData holds data for a single day's schedule
type DayScheduleData struct {
	Name               string
	Periods            []SchedulePeriodData
	IsToday            bool
	CurrentTimePercent float64
	CurrentTimeStr     string
}

// ScheduleData holds data for the schedule template
type ScheduleData struct {
	Days []DayScheduleData
}

// RenderSchedule renders the schedule template with data from loadedValues
func RenderSchedule(loadedValues *LoadedValues) (string, error) {
	days := []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
	daySchedules := make(map[string][]SchedulePeriod)

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

		blocks, err := computeScheduleBlocks(periods)
		if err != nil {
			return "", fmt.Errorf("failed to compute schedule blocks for %s: %w", day, err)
		}
		daySchedules[day] = blocks
	}

	if err := addCrossoverBlocks(daySchedules, days); err != nil {
		return "", fmt.Errorf("failed to add crossover blocks: %w", err)
	}

	now := time.Now()
	currentDay := now.Weekday().String()
	currentHour := now.Hour()
	currentMinute := now.Minute()
	currentTimeMinutes := timeToMinutes(currentHour, currentMinute)
	currentTimeStr := fmt.Sprintf("%02d:%02d", currentHour, currentMinute)
	currentTimePercent := float64(currentTimeMinutes) / float64(24*60) * 100

	data := ScheduleData{
		Days: make([]DayScheduleData, 0, len(days)),
	}

	for _, day := range days {
		dayData := DayScheduleData{
			Name:               day,
			IsToday:            day == currentDay,
			CurrentTimePercent: currentTimePercent,
			CurrentTimeStr:     currentTimeStr,
		}

		periods := daySchedules[day]
		totalMinutes := 24 * 60

		for _, period := range periods {
			heightPercent := float64(period.Duration) / float64(totalMinutes) * 100

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

			dayData.Periods = append(dayData.Periods, SchedulePeriodData{
				StartTime:     period.StartTime,
				EndTime:       period.EndTime,
				Activity:      period.Activity,
				HeightPercent: heightPercent,
				DurationStr:   durationStr,
			})
		}

		data.Days = append(data.Days, dayData)
	}

	var buf bytes.Buffer
	if err := scheduleTemplate.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute schedule template: %w", err)
	}
	return buf.String(), nil
}
