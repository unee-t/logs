package main

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"text/template"
	"time"

	"github.com/apex/log"
	jsonhandler "github.com/apex/log/handlers/json"
	"github.com/apex/log/handlers/text"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/endpoints"
	"github.com/aws/aws-sdk-go-v2/aws/external"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/gorilla/mux"
	"github.com/tidwall/pretty"
)

var views = template.Must(template.ParseGlob("templates/*.html"))

func main() {

	if os.Getenv("UP_STAGE") == "" {
		log.SetHandler(text.Default)
	} else {
		log.SetHandler(jsonhandler.Default)
	}

	addr := ":" + os.Getenv("PORT")
	app := mux.NewRouter()
	app.HandleFunc("/", index)
	app.HandleFunc("/l", loglookup)
	if err := http.ListenAndServe(addr, app); err != nil {
		log.WithError(err).Fatal("error listening")
	}

}

func index(w http.ResponseWriter, r *http.Request) {
	err := views.ExecuteTemplate(w, "index.html", nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

}

func loglookup(w http.ResponseWriter, r *http.Request) {
	uuid := r.URL.Query().Get("uuid")

	if uuid == "" {
		http.Error(w, "Empty string", http.StatusBadRequest)
		return
	}

	since := r.URL.Query().Get("since")

	hours, err := strconv.Atoi(since)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cfg, err := external.LoadDefaultAWSConfig(external.WithSharedConfigProfile("uneet-dev"))
	if err != nil {
		log.WithError(err).Fatal("setting up credentials")
		return
	}
	cfg.Region = endpoints.ApSoutheast1RegionID
	svc := cloudwatchlogs.New(cfg)
	from := time.Now().Add(-time.Hour*time.Duration(hours)).Unix() * 1000
	log.WithField("from", from).Infof("last %d hours", hours)

	req := svc.FilterLogEventsRequest(&cloudwatchlogs.FilterLogEventsInput{
		FilterPattern: aws.String(fmt.Sprintf(`{ $.fields.actionType.mefeAPIRequestId = "%s" }`, uuid)),
		LogGroupName:  aws.String("/aws/lambda/alambda_simple"),
		StartTime:     &from,
	})

	var logs []string
	p := req.Paginate()
	for p.Next() {
		page := p.CurrentPage()
		for _, event := range page.Events {
			logs = append(logs, fmt.Sprintf("%s", pretty.Pretty([]byte(*event.Message))))
		}
	}
	if err := p.Err(); err != nil {
		panic(err)
	}

	err = views.ExecuteTemplate(w, "logoutput.html", struct {
		Logs  []string
		UUID  string
		Hours int
	}{
		logs,
		uuid,
		hours,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

}
