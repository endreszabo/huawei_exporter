package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"net/http"
	"net/http/cookiejar"
	"net/url"

	log "github.com/sirupsen/logrus"
)

var opticInfoArray = []byte("var opticInfos = new Array(new stOpticInfo(")
var deviceCpuUsed = []byte("var cpuUsed = '")
var deviceMemUsed = []byte("var memUsed = '")
var deviceCpuTemp = []byte("var CpuTemperature = '")

var loggedOutBuf = []byte("<title>Waiting...")

var metrics map[string]*prometheus.Gauge

type envDef struct {
	Username      string `env:"USERNAME" envDefault:"telecomadmin"`
	Password      string `env:"PASSWORD" envDefault:"admintelecom"`
	OntAddress    string `env:"ONT_ADDRESS" envDefault:"192.168.100.1"`
	ListenAddress string `env:"LISTEN_ADDRESS" envDefault:"[::]:19143"`
}

var config envDef

func AddMetrics(name string, help string) {
	gauge := promauto.NewGauge(prometheus.GaugeOpts{
		Name: name,
		Help: help,
	})
	metrics[name] = &gauge
}

func login(c *http.Client) (err error) {
	log.Info("logging in")
	rsp, err := c.Get(fmt.Sprintf("http://%s/asp/GetRandCount.asp", config.OntAddress))
	if err != nil {
		return
	}
	b, _ := io.ReadAll(rsp.Body)
	rsp.Body.Close()
	csrf := string(b)
	csrf = csrf[3:]
	payload := url.Values{
		"UserName":     []string{config.Username},
		"PassWord":     []string{config.Password},
		"Language":     []string{"english"},
		"x.X_HW_Token": []string{csrf},
	}
	_, err = c.PostForm(fmt.Sprintf("http://%s/login.cgi", config.OntAddress), payload)
	if err != nil {
		log.Error(err)
		return
	}
	/* TODO: check if login is successful
	b, err = io.ReadAll(rsp.Body)
	if err != nil {
		return
	}
	rsp.Body.Close()
	*/
	url, err := url.Parse(fmt.Sprintf("http://%s/", config.OntAddress))
	if err != nil {
		log.Panic("error parsing URL")
	}
	cookies := c.Jar.Cookies(url)
	if len(cookies) < 1 {
		log.Panic("login failed")
	} else {
		log.Info("login successful")
	}
	return
}

func unescapeLiteral(s string) (rv string) {
	rv = strings.ReplaceAll(s, "\\x20", " ")
	rv = strings.ReplaceAll(rv, "\\x2e", ".")
	rv = strings.ReplaceAll(rv, "\\x2d", "-")
	rv = strings.Trim(rv, " ")
	return
}

func UpdateMetrics(s string, metricsName string) (err error) {
	raw := s
	if strings.Contains(s, "\\") {
		s = unescapeLiteral(s)
	}

	if metrics, ok := metrics[metricsName]; ok {
		var value float64
		value, err = strconv.ParseFloat(s, 64)
		if err != nil {
			log.WithFields(log.Fields{
				"value":         value,
				"raw_string":    raw,
				"parsed_string": s,
				"metrics_name":  metricsName,
				"err":           err,
			}).Error("could not parse value")
			return
		}
		(*metrics).Set(value)
		log.WithFields(log.Fields{
			"value":         value,
			"raw_string":    raw,
			"parsed_string": s,
			"metrics_name":  metricsName,
		}).Info("updated metric")

	} else {
		log.WithFields(log.Fields{
			"metrics_name": metricsName,
		}).Error("metrics with given name could not be found")
	}
	return
}

func SetupMetrics() {
	AddMetrics("ont_optical_power_tx_dbm", "Optical TX Power in dBm")
	AddMetrics("ont_optical_power_rx_dbm", "Optical RX Power in dBm")
	AddMetrics("ont_optical_working_voltage_mv", "Optics working voltage in mV")
	AddMetrics("ont_optical_working_temperature_c", "Optics temperature")
	AddMetrics("ont_optical_bias_mv", "Optics bias current in mA")
	AddMetrics("system_cpu_usage_pct", "CPU usage in percent")
	AddMetrics("system_mem_usage_pct", "Memory usage in percent")
	AddMetrics("system_cpu_temp_c", "CPU temperature")
}

func fetch(c *http.Client) (err error) {
	rsp, err := c.Get(fmt.Sprintf("http://%s/html/amp/opticinfo/opticinfo.asp", config.OntAddress))
	if err != nil {
		return
	}

	scanner := bufio.NewScanner(rsp.Body)
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		b := scanner.Bytes()
		if len(b) > len(loggedOutBuf) && bytes.Equal(b[:len(loggedOutBuf)], loggedOutBuf) {
			rsp.Body.Close()
			return fmt.Errorf("logged out")
		}

		if len(b) > len(opticInfoArray) && bytes.Equal(b[:len(opticInfoArray)], opticInfoArray) {
			s := scanner.Text()
			sp := strings.SplitN(s, "\"", 15)

			UpdateMetrics(sp[5], "ont_optical_power_tx_dbm")
			UpdateMetrics(sp[7], "ont_optical_power_rx_dbm")
			UpdateMetrics(sp[9], "ont_optical_working_voltage_mv")
			UpdateMetrics(sp[11], "ont_optical_working_temperature_c")
			UpdateMetrics(sp[13], "ont_optical_bias_mv")
			break
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		log.WithFields(log.Fields{
			"scanner": "first",
		}).Error(scanErr)
		return scanErr
	}

	rsp.Body.Close()

	rsp, err = c.Get(fmt.Sprintf("http://%s/html/ssmp/deviceinfo/deviceinfocut.asp", config.OntAddress))
	if err != nil {
		log.Error(err)
		return
	}

	scanner = bufio.NewScanner(rsp.Body)
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		b := scanner.Bytes()
		if len(b) > len(loggedOutBuf) && bytes.Equal(b[:len(loggedOutBuf)], loggedOutBuf) {
			rsp.Body.Close()
			return fmt.Errorf("logged out")
		}
		if len(b) > len(deviceCpuUsed) && bytes.Equal(b[:len(deviceCpuUsed)], deviceCpuUsed) {
			s := scanner.Text()
			sp := strings.SplitN(s, "'", 15)
			sp[1] = strings.Replace(sp[1], "%", "", 1)
			UpdateMetrics(sp[1], "system_cpu_usage_pct")
		} else if len(b) > len(deviceMemUsed) && bytes.Equal(b[:len(deviceMemUsed)], deviceMemUsed) {
			s := scanner.Text()
			sp := strings.SplitN(s, "'", 15)
			sp[1] = strings.Replace(sp[1], "%", "", 1)
			UpdateMetrics(sp[1], "system_mem_usage_pct")
		} else if len(b) > len(deviceCpuTemp) && bytes.Equal(b[:len(deviceCpuTemp)], deviceCpuTemp) {
			s := scanner.Text()
			sp := strings.SplitN(s, "'", 15)
			UpdateMetrics(sp[1], "system_cpu_temp_c")
			break
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		log.WithFields(log.Fields{
			"scanner": "first",
		}).Error(scanErr)
		return scanErr
	}

	return
}

func main() {
	err := env.ParseWithOptions(&config, env.Options{
		Prefix: "HUAWEI_EXPORTER_",
	})
	if err != nil {
		log.Panic(err)
	}

	config.Password = base64.StdEncoding.EncodeToString([]byte(config.Password))

	metrics = make(map[string]*prometheus.Gauge)
	SetupMetrics()
	log.SetLevel(log.WarnLevel)

	jar, err := cookiejar.New(nil)
	if err != nil {
		log.Error(err)
		panic(err)
	}

	client := &http.Client{
		Jar: jar,
	}
	ticker := time.NewTicker(5 * time.Second)
	done := make(chan bool)
	go func() {
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				err = fetch(client)
				if err != nil {
					if err.Error() == "logged out" {
						login(client)
						err = fetch(client)
						if err != nil {
							log.Error(err)
							panic(err)
						}
					} else {
						log.Error(err)
						panic(err)
					}
				}
			}
		}
	}()

	http.Handle("/metrics", promhttp.Handler())
	err = http.ListenAndServe(config.ListenAddress, nil)
	if err != nil {
		log.Error(err)
		panic(err)
	}
}
