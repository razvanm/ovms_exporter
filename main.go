package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"v.io/x/lib/vlog"
)

var (
	addrFlag         = flag.String("addr", ":8080", "Address to listen on")
	usernameFlag     = flag.String("username", "", "OVMS server username")
	passwordFlag     = flag.String("password", "", "OVMS server password")
	vehicleIDFlag    = flag.String("vehicle", "", "OVMS server password")
	ovmsSeverFlag    = flag.String("server", "api.openvehicles.com:6868", "OVMS server")
	pollDurationFlag = flag.Duration("poll-duration", time.Minute, "How frequently to poll OVMS server")
)

type record struct {
	Code     string `json:"m_code"`
	Msg      string `json:"m_msg"`
	MsgTime  string `json:"m_msgtime"`
	Paranoid int    `json:"m_paranoid"`
	PToken   string `json:"m_ptoken"`
}

// Reference: https://github.com/openvehicles/Open-Vehicle-Monitoring-System-3/blob/0f16f531cb7dac8aa3d256fe3f42fde4da52000f/vehicle/OVMS.V3/components/ovms_server_v2/src/ovms_server_v2.cpp#L1007-L1088
var sMetrics = []string{
	"ms_v_bat_soc",                     //      1	StandardMetrics.ms_v_bat_soc->AsString("0", Other, 1)
	"m_units_distance",                 //      2	((m_units_distance == Kilometers) ? "K" : "M")
	"ms_v_charge_voltage",              //      3	StandardMetrics.ms_v_charge_voltage->AsInt()
	"ms_v_charge_current",              //      4	StandardMetrics.ms_v_charge_current->AsFloat()
	"ms_v_charge_state",                //      5	StandardMetrics.ms_v_charge_state->AsString("stopped")
	"ms_v_charge_mode",                 //      6	StandardMetrics.ms_v_charge_mode->AsString("standard")
	"ms_v_bat_range_ideal",             //      7	StandardMetrics.ms_v_bat_range_ideal->AsInt(0, m_units_distance)
	"ms_v_bat_range_est",               //      8	StandardMetrics.ms_v_bat_range_est->AsInt(0, m_units_distance)
	"ms_v_charge_climit",               //      9	StandardMetrics.ms_v_charge_climit->AsInt()
	"ms_v_charge_time",                 //     10	StandardMetrics.ms_v_charge_time->AsInt(0,Seconds)
	"car_charge_b4",                    //     11	"0"  // car_charge_b4
	"ms_v_charge_kwh",                  //     12	(int)(StandardMetrics.ms_v_charge_kwh->AsFloat() * 10)
	"ms_v_charge_substate",             //     13	chargesubstate_key(StandardMetrics.ms_v_charge_substate->AsString(""))
	"ms_v_charge_state",                //     14	chargestate_key(StandardMetrics.ms_v_charge_state->AsString("stopped"))
	"ms_v_charge_mode",                 //     15	chargemode_key(StandardMetrics.ms_v_charge_mode->AsString("standard"))
	"ms_v_charge_timermode",            //     16	StandardMetrics.ms_v_charge_timermode->AsBool()
	"ms_v_charge_timerstart",           //     17	StandardMetrics.ms_v_charge_timerstart->AsInt()
	"car_stale_timer",                  //     18	"0"  // car_stale_timer
	"ms_v_bat_cac",                     //     19	StandardMetrics.ms_v_bat_cac->AsFloat()
	"ms_v_charge_duration_full",        //     20	StandardMetrics.ms_v_charge_duration_full->AsInt()
	"ms_v_charge_duration_chage_limit", //     21	(((mins_range >= 0) && (mins_range < mins_soc)) ? mins_range : mins_soc)
	"ms_v_charge_limit_range",          //     22	(int) StandardMetrics.ms_v_charge_limit_range->AsFloat(0, m_units_distance)
	"ms_v_charge_limit_soc",            //     23	StandardMetrics.ms_v_charge_limit_soc->AsInt()
	"ms_v_env_cooling",                 //     24	(StandardMetrics.ms_v_env_cooling->AsBool() ? 0 : -1)
	"car_cooldown_tbattery",            //     25	"0"  // car_cooldown_tbattery
	"car_cooldown_timelimit",           //     26	"0"  // car_cooldown_timelimit
	"car_chargeestimate",               //     27	"0"  // car_chargeestimate
	"mins_range",                       //     28	mins_range
	"mins_soc",                         //     29	mins_soc
	"ms_v_bat_range_full",              //     30	StandardMetrics.ms_v_bat_range_full->AsInt(0, m_units_distance)
	"car_chargetype",                   //     31	"0"  // car_chargetype
	"ms_v_bat_power",                   //     32	(charging ? -StandardMetrics.ms_v_bat_power->AsFloat() : 0)
	"ms_v_bat_voltage",                 //     33	StandardMetrics.ms_v_bat_voltage->AsFloat()
	"ms_v_bat_soh",                     //     34	StandardMetrics.ms_v_bat_soh->AsInt()
	"ms_v_charge_power",                //     35	StandardMetrics.ms_v_charge_power->AsFloat()
	"ms_v_charge_efficiency",           //     36	StandardMetrics.ms_v_charge_efficiency->AsFloat()
	"ms_v_bat_current",                 //     37	StandardMetrics.ms_v_bat_current->AsFloat()
	"ms_v_bat_range_speed",             //     38	StandardMetrics.ms_v_bat_range_speed->AsFloat(0, units_speed)
}

// Reference: https://github.com/openvehicles/Open-Vehicle-Monitoring-System-3/blob/0f16f531cb7dac8aa3d256fe3f42fde4da52000f/vehicle/OVMS.V3/components/ovms_server_v2/src/ovms_server_v2.cpp#L1545-L1589
var dMetrics = []string{
	"doors1",                   //  1	(int)Doors1()
	"doors2",                   //  2	(int)Doors2()
	"ms_v_env_locked",          //  3	(StandardMetrics.ms_v_env_locked->AsBool()?"4":"5")
	"ms_v_inv_temp",            //  4	StandardMetrics.ms_v_inv_temp->AsString("0")
	"ms_v_mot_temp",            //  5	StandardMetrics.ms_v_mot_temp->AsString("0")
	"ms_v_bat_temp",            //  6	StandardMetrics.ms_v_bat_temp->AsString("0")
	"ms_v_pos_trip",            //  7	int(StandardMetrics.ms_v_pos_trip->AsFloat(0, m_units_distance)*10)
	"ms_v_pos_odometer",        //  8	int(StandardMetrics.ms_v_pos_odometer->AsFloat(0, m_units_distance)*10)
	"ms_v_pos_speed",           //  9	StandardMetrics.ms_v_pos_speed->AsString("0")
	"ms_v_env_parktime",        // 10	StandardMetrics.ms_v_env_parktime->AsString("0")
	"ms_v_env_temp",            // 11	StandardMetrics.ms_v_env_temp->AsString("0")
	"doors3",                   // 12	(int)Doors3()
	"stale_temps",              // 13	(stale_temps ? "0" : "1")
	"ms_v_env_temp",            // 14	(StandardMetrics.ms_v_env_temp->IsStale() ? "0" : "1")
	"ms_v_bat_12v_voltage",     // 15	StandardMetrics.ms_v_bat_12v_voltage->AsString("0")
	"doors4",                   // 16	(int)Doors4()
	"ms_v_bat_12v_voltage_ref", // 17	StandardMetrics.ms_v_bat_12v_voltage_ref->AsString("0")
	"doors5",                   // 18	(int)Doors5()
	"ms_v_charge_temp",         // 19	StandardMetrics.ms_v_charge_temp->AsString("0")
	"ms_v_bat_12v_current",     // 20	StandardMetrics.ms_v_bat_12v_current->AsString("0")
	"ms_v_env_cabintemp",       // 21	StandardMetrics.ms_v_env_cabintemp->AsString("0")
}

// Reference: https://github.com/openvehicles/Open-Vehicle-Monitoring-System-3/blob/0f16f531cb7dac8aa3d256fe3f42fde4da52000f/vehicle/OVMS.V3/components/ovms_server_v2/src/ovms_server_v2.cpp#L1217-L1255
var lMetrics = []string{
	"ms_v_pos_latitude",    //  1	StandardMetrics.ms_v_pos_latitude->AsString("0",Other,6)
	"ms_v_pos_longitude",   //  2	StandardMetrics.ms_v_pos_longitude->AsString("0",Other,6)
	"ms_v_pos_direction",   //  3	StandardMetrics.ms_v_pos_direction->AsString("0")
	"ms_v_pos_altitude",    //  4	StandardMetrics.ms_v_pos_altitude->AsString("0")
	"ms_v_pos_gpslock",     //  5	StandardMetrics.ms_v_pos_gpslock->AsBool(false)
	"stale",                //  6	((stale)?",0,":",1,")
	"ms_v_pos_speed",       //  7	StandardMetrics.ms_v_pos_speed->AsString("0", units_speed, 1)
	"ms_v_pos_trip",        //  8	int(StandardMetrics.ms_v_pos_trip->AsFloat(0, m_units_distance)*10)
	"drivemode",            //  9	drivemode
	"ms_v_bat_power",       // 10	StandardMetrics.ms_v_bat_power->AsString("0",Other,3)
	"ms_v_bat_energy_used", // 11	StandardMetrics.ms_v_bat_energy_used->AsString("0",Other,3)
	"ms_v_bat_energy_recd", // 12	StandardMetrics.ms_v_bat_energy_recd->AsString("0",Other,3)
	"ms_v_inv_power",       // 13	StandardMetrics.ms_v_inv_power->AsFloat()
	"ms_v_inv_efficiency",  // 14	StandardMetrics.ms_v_inv_efficiency->AsFloat()
	"ms_v_pos_gpsmode",     // 15	StandardMetrics.ms_v_pos_gpsmode->AsString()
	"ms_v_pos_satcount",    // 16	StandardMetrics.ms_v_pos_satcount->AsInt()
	"ms_v_pos_gpshdop",     // 17	StandardMetrics.ms_v_pos_gpshdop->AsString("0", Native, 1)
	"ms_v_pos_gpsspeed",    // 18	StandardMetrics.ms_v_pos_gpsspeed->AsString("0", units_speed, 1)
	"ms_v_pos_gpssq",       // 19	StandardMetrics.ms_v_pos_gpssq->AsInt()
}

// Reference: https://github.com/openvehicles/Open-Vehicle-Monitoring-System-3/blob/0f16f531cb7dac8aa3d256fe3f42fde4da52000f/vehicle/OVMS.V3/components/ovms_server_v2/src/ovms_server_v2.cpp#L1298-L1326
var wMetrics = []string{
	"wheels_count",             //  1	wheels.size();
	"wheel1",                   //  2	wheel1
	"wheel2",                   //  3	wheel2
	"wheel3",                   //  4	wheel3
	"wheel4",                   //  5	wheel4
	"ms_v_tpms_pressure_count", //  6	StandardMetrics.ms_v_tpms_pressure->GetSize()
	"ms_v_tpms_pressure",       //  7	StandardMetrics.ms_v_tpms_pressure->AsString("", kPa, 1)
	"defstale_pressure",        //  8	defstale_pressure
	"ms_v_tpms_temp_count",     //  9	StandardMetrics.ms_v_tpms_temp->GetSize()
	"ms_v_tpms_temp",           // 10	StandardMetrics.ms_v_tpms_temp->AsString("", Celcius, 1)
	"defstale_temp",            // 11	defstale_temp
	"ms_v_tpms_health_count",   // 12	StandardMetrics.ms_v_tpms_health->GetSize()
	"ms_v_tpms_health",         // 13	StandardMetrics.ms_v_tpms_health->AsString("", Percentage, 1)
	"defstale_health",          // 14	defstale_health
	"ms_v_tpms_alert_count",    // 15	StandardMetrics.ms_v_tpms_alert->GetSize()
	"ms_v_tpms_alert",          // 16	StandardMetrics.ms_v_tpms_alert->AsString("")
	"defstale_alert",           // 17	defstale_alert
}

var metricsMap = map[string][]string{
	"S": sMetrics,
	"D": dMetrics,
	"L": lMetrics,
	"W": wMetrics,
}

func fetch() []byte {
	urlPrefix := fmt.Sprintf("http://%s/api/protocol/%s", *ovmsSeverFlag, *vehicleIDFlag)
	resp, err := http.Get(fmt.Sprintf("%s?username=%s&password=%s", urlPrefix, url.QueryEscape(*usernameFlag), url.QueryEscape(*passwordFlag)))
	if err != nil {
		vlog.Errorf("Error fetching %q: %v", urlPrefix, err)
		return nil
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		vlog.Errorf("Error reding the response for %q: %v", urlPrefix, err)
		return nil
	}

	return body
}

func promMetric(name string, val string, ts time.Time) string {
	tsMillis := ts.UnixMilli()
	if _, err := strconv.ParseFloat(val, 64); err != nil {
		// Put the non-numeric value in the label.
		return fmt.Sprintf("%s{value=%q} 1 %d", name, val, tsMillis)
	}

	return fmt.Sprintf("%s %s %d", name, val, tsMillis)
}

func fetchMetrics() string {
	var metrics []string

	data := fetch()
	if data == nil || len(data) == 0 {
		return ""
	}

	records := []record{}
	if err := json.Unmarshal(data, &records); err != nil {
		vlog.Errorf("JSON error unmashaling %q: ", string(data), err)
		return ""
	}

	vlog.Infof("num records: %d", len(records))

	for _, rec := range records {
		ts, err := time.ParseInLocation("2006-01-02 15:04:05", rec.MsgTime, time.UTC)
		if err != nil {
			vlog.Errorf("Error parsing time %q from record %q: %v", rec.MsgTime, rec, err)
			continue
		}

		data := strings.Split(rec.Msg, ",")
		vlog.Infof("%v: %q", ts, data)

		if m, ok := metricsMap[rec.Code]; ok {
			for i, val := range data {
				vlog.VI(1).Infof("%s [%d]: %s=%q", ts, i, m[i], val)
				metrics = append(metrics, promMetric(fmt.Sprintf("ovms_%s_%s", rec.Code, m[i]), val, ts))
			}
		}
	}

	return strings.Join(metrics, "\n") + "\n"
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
