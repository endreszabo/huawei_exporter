package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"net/http"
	"net/http/cookiejar"
	"net/url"
)

const USERNAME = "telecomadmin"
const PASSWORD = "YWRtaW50ZWxlY29t"
const HOST = "192.168.100.1"

var opticInfoArray = []byte("var opticInfos = new Array(new stOpticInfo(")
var deviceCpuUsed = []byte("var cpuUsed = '")
var deviceMemUsed = []byte("var memUsed = '")
var deviceCpuTemp = []byte("var CpuTemperature = '")

var loggedOutBuf = []byte("<title>Waiting...")

var (
	opticalTxPower = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ont_optical_power_tx_dbm",
		Help: "Optical TX Power in dBm",
	})
	opticalRxPower = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ont_optical_power_rx_dbm",
		Help: "Optical RX Power in dBm",
	})
	opticalWorkingVoltagemV = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ont_optical_working_voltage_mv",
		Help: "Optics working voltage in mV",
	})
	opticalTemperature = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ont_optical_working_temperature_c",
		Help: "Optics temperature",
	})
	opticalBiasCurrentMa = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ont_optical_bias_mv",
		Help: "Optics bias current in mA",
	})
	systemCpuUsage = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "system_cpu_usage_pct",
		Help: "CPU usage in percent",
	})
	systemMemUsage = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "system_mem_usage_pct",
		Help: "Memory usage in percent",
	})
	systemTemperature = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "system_cpu_temp_c",
		Help: "CPU temperature",
	})
)

func login(c *http.Client) (err error) {
	fmt.Println("logging in")
	rsp, err := c.Get(fmt.Sprintf("http://%s/asp/GetRandCount.asp", HOST))
	if err != nil {
		return
	}
	b, _ := io.ReadAll(rsp.Body)
	rsp.Body.Close()
	csrf := string(b)
	csrf = csrf[3:]
	payload := url.Values{
		"UserName":     []string{USERNAME},
		"PassWord":     []string{PASSWORD},
		"Language":     []string{"english"},
		"x.X_HW_Token": []string{csrf},
	}
	_, err = c.PostForm("http://192.168.100.1/login.cgi", payload)
	if err != nil {
		return
	}
	/* FIXME: check is login is successful
	b, err = io.ReadAll(rsp.Body)
	if err != nil {
		return
	}
	rsp.Body.Close()
	*/
	return
}

func unescapeLiteral(s string) (rv string) {
	rv = strings.ReplaceAll(s, "\\x20", " ")
	rv = strings.ReplaceAll(rv, "\\x2e", ".")
	rv = strings.ReplaceAll(rv, "\\x2d", "-")
	rv = strings.Trim(rv, " ")
	return
}

func strLiteralToFloat(b string) (fl float64, err error) {
	fok := unescapeLiteral(b)
	fl, err = strconv.ParseFloat(fok, 64)
	return
}

func fetch(c *http.Client) (err error) {
	rsp, err := c.Get(fmt.Sprintf("http://%s/html/amp/opticinfo/opticinfo.asp", HOST))
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
			fl, err := strLiteralToFloat(sp[5])
			if err != nil {
				return err
			}
			opticalTxPower.Set(fl)

			fl, err = strLiteralToFloat(sp[7])
			opticalRxPower.Set(fl)
			if err != nil {
				return err
			}

			fl, err = strconv.ParseFloat(sp[9], 64)
			if err != nil {
				return err
			}
			opticalWorkingVoltagemV.Set(fl)

			fl, err = strconv.ParseFloat(sp[11], 64)
			if err != nil {
				return err
			}
			opticalTemperature.Set(fl)

			fl, err = strconv.ParseFloat(sp[13], 64)
			if err != nil {
				return err
			}
			opticalBiasCurrentMa.Set(fl)
			break
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return scanErr
	}

	rsp.Body.Close()

	rsp, err = c.Get(fmt.Sprintf("http://%s/html/ssmp/deviceinfo/deviceinfocut.asp", HOST))
	if err != nil {
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
			cpuUsage, err := strconv.ParseFloat(strings.Replace(sp[1], "%", "", 1), 64)
			if err != nil {
				return err
			}
			systemCpuUsage.Set(cpuUsage)
		} else if len(b) > len(deviceMemUsed) && bytes.Equal(b[:len(deviceMemUsed)], deviceMemUsed) {
			s := scanner.Text()
			sp := strings.SplitN(s, "'", 15)
			memUsage, err := strconv.ParseFloat(strings.Replace(sp[1], "%", "", 1), 64)
			if err != nil {
				return err
			}
			systemMemUsage.Set(memUsage)
		} else if len(b) > len(deviceCpuTemp) && bytes.Equal(b[:len(deviceCpuTemp)], deviceCpuTemp) {
			s := scanner.Text()
			sp := strings.SplitN(s, "'", 15)
			cpuTemp, err := strconv.ParseFloat(sp[1], 64)
			if err != nil {
				return err
			}
			systemTemperature.Set(cpuTemp)
			break
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return scanErr
	}

	return
}

func main() {

	jar, err := cookiejar.New(nil)
	if err != nil {
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
							panic(err)
						}
					} else {
						panic(err)
					}
				}
			}
		}
	}()
	/*
		time.Sleep(60 * time.Second)
		ticker.Stop()
		done <- true
		fmt.Println("Ticker stopped")
	*/
	http.Handle("/metrics", promhttp.Handler())
	err = http.ListenAndServe("127.0.0.1:9443", nil)
	if err != nil {
		panic(err)
	}
	//c.Visit(fmt.Sprintf("http://%s/asp/GetRandCount.asp", HOST))
}
