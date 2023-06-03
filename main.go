package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"v.io/x/lib/vlog"
)

var (
	addrFlag         = flag.String("addr", ":8080", "Address to listen on")
	filterFlag       = flag.String("filter", "^v_b_12v_voltage$", "Regular expression to use for filtering the exported metrics")
	usernameFlag     = flag.String("username", "", "OVMS server username")
	passwordFlag     = flag.String("password", "", "OVMS server password")
	vehicleIDFlag    = flag.String("vehicle", "", "OVMS server password")
	ovmsSeverFlag    = flag.String("server", "api.openvehicles.com:6868", "OVMS server")
	pollDurationFlag = flag.Duration("poll-duration", 20*time.Minute, "How frequently to poll OVMS server")
)

type record struct {
	Data         string `json:"h_data"`
	RecordNumber int    `json:"h_recordnumber"`
	Timestamp    string `json:"h_timestamp"`
}

// Prometheus doesn't like "." in the metric names.
func normalize(in []string) []string {
	r := make([]string, len(in))
	for i, val := range in {
		r[i] = strings.ReplaceAll(val, ".", "_")
	}
	return r
}

var dMetrics = normalize([]string{
	"",                    //      1	Door state #1
	"",                    //      2	Door state #2
	"",                    //      3	Lock/Unlock state
	"",                    //      4	Temperature of the PEM (celcius)
	"v.m.temp",            //      5	Temperature of the Motor (celcius)
	"v.b.temp",            //      6	Temperature of the Battery (celcius)
	"v.p.trip",            //      7	Car trip meter (in 1/10th of a distance unit)
	"v.p.odometer",        //      8	Car odometer (in 1/10th of a distance unit)
	"v.p.speed",           //      9	Car speed (in distance units per hour)
	"v.e.parktime",        //     10	Car parking timer (0 for not parked, or number of seconds car parked for)
	"v.e.temp",            //     11	Ambient Temperature (in Celcius)
	"",                    //     12	Door state #3
	"",                    //     13	Stale PEM,Motor,Battery temps indicator (-1=none, 0=stale, >0 ok)
	"",                    //     14	Stale ambient temp indicator (-1=none, 0=stale, >0 ok)
	"v.b.12v.voltage",     //     15	Vehicle 12V line voltage
	"",                    //     16	Door State #4
	"v.b.12v.voltage.ref", //     17	Reference voltage for 12v power
	"",                    //     18	Door State #5
	"v.c.temp",            //     19	Temperature of the Charger (celsius)
	"v.b.12v.current",     //     20	Vehicle 12V current (i.e. DC converter output)
	"v.e.cabintemp",       //     21	Cabin temperature (celsius)
})

func fetch() []byte {
	urlPrefix := fmt.Sprintf("http://%s/api/historical/%s/D", *ovmsSeverFlag, *vehicleIDFlag)
	resp, err := http.Get(fmt.Sprintf("%s?username=%s&password=%s", urlPrefix, url.QueryEscape(*usernameFlag), url.QueryEscape(*passwordFlag)))
	if err != nil {
		vlog.Errorf("Error fetching %q: %v", urlPrefix, err)
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		vlog.Errorf("Error reding the response for %q: %v", urlPrefix, err)
	}

	return body
}

func fetchMetrics() string {
	metrics := make(map[string][]string)

	data := fetch()
	records := []record{}
	if err := json.Unmarshal(data, &records); err != nil {
		vlog.Errorf("JSON error unmashaling %q: ", string(data), err)
		return ""
	}

	vlog.Infof("num records: %d", len(records))
	filter := regexp.MustCompile(*filterFlag)
	vlog.Infof("filter: %q", *filterFlag)

	for _, rec := range records {
		ts, err := time.ParseInLocation("2006-01-02 15:04:05", rec.Timestamp, time.UTC)
		if err != nil {
			vlog.Fatalf("Error parsing time %q from record %q: %v", rec.Timestamp, rec, err)
		}
		data := strings.Split(rec.Data, ",")
		vlog.Infof("%v: %q", ts, data)
		for i, val := range data {
			vlog.Infof("%s [%d]: %s=%q", ts, i, dMetrics[i], val)
			m := dMetrics[i]
			if filter.MatchString(m) {
				metrics[m] = append(metrics[m], fmt.Sprintf("%s %s %d", dMetrics[i], val, ts.UnixMilli()))
			}
		}
	}

	var r string
	for m, s := range metrics {
		r += fmt.Sprintf("# TYPE %s gauge\n%s\n", m, strings.Join(s, "\n"))
	}
	return r
}

func main() {
	flag.Parse()
	vlog.ConfigureLibraryLoggerFromFlags()

	var metricsText string
	var mu sync.RWMutex

	go func() {
		for {
			m := fetchMetrics()
			if m != "" {
				mu.Lock()
				metricsText = m
				mu.Unlock()
			}
			vlog.Infof("Sleep for %v...", *pollDurationFlag)
			time.Sleep(*pollDurationFlag)
		}
	}()

	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		mu.RLock()
		m := metricsText
		mu.RUnlock()
		fmt.Fprintf(w, m)
	})
	vlog.Fatal(http.ListenAndServe(*addrFlag, nil))
}
