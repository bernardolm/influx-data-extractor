package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"github.com/urfave/cli"
	"github.com/valyala/fasthttp"
	"golang.org/x/sync/semaphore"
)

type intervalControl struct {
	sync.RWMutex

	duration time.Duration
	from     time.Time
	now      time.Time
	to       time.Time
}

func (ic *intervalControl) nextInterval() {
	ic.Lock()
	defer ic.Unlock()

	ic.to = ic.to.Add(ic.duration * -1)
	ic.from = ic.to.Add(ic.duration * -1)
}

func newIntervalControl(d time.Duration) *intervalControl {
	ic := intervalControl{
		duration: d,
		now:      time.Now(),
	}
	ic.Lock()
	defer ic.Unlock()
	ic.to = time.Date(ic.now.Year(), ic.now.Month(), ic.now.Day(), ic.now.Hour(), 0, 0, 0, time.UTC)
	ic.from = ic.to.Add(ic.duration * -1)
	return &ic
}

func configEvs() {
	viper.SetConfigFile("config.yml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("../")
	if err := viper.ReadInConfig(); err != nil {
		logrus.Info("config file not found, running using OS env vars")
	}
	viper.AutomaticEnv()

	if !viper.IsSet("INFLUX_API_URL") {
		logrus.Fatal("failed to get INFLUX_API_URL env")
	}
}

func flatQuery(query string) string {
	return strings.Replace(strings.Replace(strings.TrimSpace(query), "\t", "", -1), "\n", "", -1)
}

func downloadReport(from time.Time, to time.Time) ([]byte, error) {
	query := fmt.Sprintf(viper.GetString("INFLUX_QUERY")+" AND time >= '%s' AND time < '%s'",
		from.Format(time.RFC3339), to.Format(time.RFC3339))

	logrus.Debugf("query executed: %s", flatQuery(query))

	values := url.Values{}
	values.Set("db", viper.GetString("INFLUX_DATABASE"))
	values.Set("q", query)

	req := fasthttp.AcquireRequest()
	req.Header.Add("Accept", "application/csv")
	req.Header.Add("Accept-Encoding", "gzip")
	req.Header.SetContentType("application/x-www-form-urlencoded")
	req.Header.SetHost(viper.GetString("INFLUX_API_URL"))
	req.Header.SetMethod("POST")
	req.SetBodyString(values.Encode())
	req.SetRequestURI(viper.GetString("INFLUX_API_URL") + "/query?pretty=true")

	res := fasthttp.AcquireResponse()
	client := &fasthttp.Client{}

	logrus.WithField("from", from.Format(time.RFC3339)).
		WithField("to", to.Format(time.RFC3339)).
		Info("downloading from influx API...")

	err := client.Do(req, res)
	logrus.Debugf("HTTP log:\n%s\n\nResponse received: %d bytes", req.String(), len(res.String()))
	if err != nil {
		logrus.WithError(err).Error("failed to download from influx, try again...")
		time.Sleep(5 * time.Second)
		return downloadReport(from, to)
	}

	data, err := res.BodyGunzip()
	if err != nil {
		logrus.WithError(err).Error("failed to decode gzip")
		return nil, err
	}

	return data, nil
}

func main() {
	configEvs()

	if viper.GetBool("DEBUG") {
		logrus.Debugf("executing in debug mode")
		logrus.SetLevel(logrus.DebugLevel)
	}

	var maxParallel int64
	var repeatParam int
	var timeDurationParam string

	app := cli.NewApp()

	app.Flags = []cli.Flag{
		cli.StringFlag{
			Destination: &timeDurationParam,
			Name:        "interval",
			Usage:       "Positive time interval that report files will contain. e.g. 30m, 1h, 60s...",
			Value:       "1h",
		},
		cli.IntFlag{
			Destination: &repeatParam,
			Name:        "repeat",
			Usage:       "How many times will interval be queried",
			Value:       24,
		},
		cli.Int64Flag{
			Destination: &maxParallel,
			Name:        "max-parallel",
			Usage:       "Max parallel workers",
			Value:       int64(runtime.NumCPU() / 2),
		},
	}

	app.Action = func(c *cli.Context) error {
		duration, err := time.ParseDuration(timeDurationParam)
		if err != nil {
			logrus.WithError(err).Error("failed to parse duration param")
			return err
		}

		ctx := context.Background()
		ic := newIntervalControl(duration)
		sem := semaphore.NewWeighted(maxParallel)
		wg := sync.WaitGroup{}

		logrus.WithField("interval", timeDurationParam).WithField("repeat", repeatParam).
			WithField("max-parallel", maxParallel).Info("starting...")

		for i := 0; i <= repeatParam; i++ {
			filename := fmt.Sprintf("influx_from-%s_to-%s", ic.from.Format("20060102150405"),
				ic.to.Format("20060102150405"))

			logrus.Debugf("checking file %s", filename)

			if err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if info.Mode().IsRegular() && strings.Contains(info.Name(), filename) {
					return errors.New("report already generated, skipping")
				}
				return nil
			}); err != nil {
				if msg := err.Error(); strings.Contains(msg, "already generated") {
					logrus.WithField("from", ic.from.Format(time.RFC3339)).WithField("filename", filename).
						WithField("to", ic.to.Format(time.RFC3339)).Warn(err.Error())
					ic.nextInterval()
					continue
				} else {
					return err
				}
			}

			if err := sem.Acquire(ctx, 1); err != nil {
				logrus.WithError(err).Error("failed to acquire semaphore")
				return err
			}

			lic := ic
			wg.Add(1)

			go func(filename string, from time.Time, to time.Time, now time.Time) {
				defer sem.Release(1)
				defer wg.Done()

				entry := logrus.WithField("from", from.Format(time.RFC3339)).WithField("to", to.Format(time.RFC3339)).
					WithField("filename", filename)

				body := make([]byte, 0)
				var err error
				if body, err = downloadReport(from, to); err != nil {
					entry.WithError(err).Error("failed to download file")
					return
				}

				filename = fmt.Sprintf("reports/%s_at-%s.csv", filename, now.Format("20060102150405"))

				if err := ioutil.WriteFile(filename, body, 0644); err != nil {
					entry.WithError(err).Error("failed to write file")
					return
				}

				entry.Info("report generated")

				return
			}(filename, lic.from, lic.to, lic.now)

			ic.nextInterval()
		}

		wg.Wait()

		logrus.Infof("all process finished after: %v", time.Now().Sub(ic.now))

		return nil
	}

	if err := app.Run(os.Args); err != nil {
		logrus.Fatal(err)
	}
}
