package cmd

import "encoding/xml"

// Updates is the root element of the OTA manifest XML.
type Updates struct {
	XMLName xml.Name `xml:"updates"`
	XMLNS   string   `xml:"xmlns,attr"`
	Updates []Update `xml:"update"`
}

// Update represents a single firmware update entry.
type Update struct {
	Type         string       `xml:"type"`
	Model        string       `xml:"model"`
	Locales      Locales      `xml:"locales"`
	Version      string       `xml:"version"`
	URL          string       `xml:"url"`
	ReleaseNotes ReleaseNotes `xml:"releaseNotes"`
}

// Locales contains the list of supported locales for an update.
type Locales struct {
	Locales []string `xml:"locale"`
}

// ReleaseNotes contains URLs to release notes in various formats.
type ReleaseNotes struct {
	URLs []ReleaseNoteURL `xml:"url"`
}

// ReleaseNoteURL represents a release note URL with content type and locale.
type ReleaseNoteURL struct {
	Type   string `xml:"type,attr"`
	Locale string `xml:"locale,attr"`
	URL    string `xml:",chardata"`
}
