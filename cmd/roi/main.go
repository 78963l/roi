package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gorilla/securecookie"
	"github.com/studio2l/roi"
)

var (
	// dev는 현재 개발모드인지를 나타낸다.
	dev bool

	// DB는 http 핸들러 안에서 사용할 DB 커넥션 풀이다.
	// 동시에 여러 고루틴에서 사용해도 안전하다.
	DB *sql.DB
)

func redirectToHttps(w http.ResponseWriter, r *http.Request) {
	to := "https://" + strings.Split(r.Host, ":")[0] + r.URL.Path
	if r.URL.RawQuery != "" {
		to += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, to, http.StatusTemporaryRedirect)
}

func main() {
	dev = true

	var (
		addr     string
		insecure bool
		cert     string
		key      string
		dbAddr   string
		dbCA     string
		dbCert   string
		dbKey    string
	)
	addrDefault := "localhost:80:443"
	addrHelp := `binding address and it's http/https port.

when two ports are specified, first port is for http and second is for https.
and automatically forward http access to https, if using default https protocol.
when -insecure flag is on, it will use only the first port.
ex) localhost:80:443

when only one port is specified, the port will be used for current protocol.
ex) localhost:80, localhost:443

when no port is specified, it is same as :80:443.
ex) localhost

when ROI_ADDR environment variable is not empty, it will use the value as default.

`
	addrEnv := os.Getenv("ROI_ADDR")
	if addrEnv != "" {
		addrDefault = addrEnv
		addrHelp += "currently the default value is comming from ROI_ADDR"
	}
	flag.StringVar(&addr, "addr", addrDefault, addrHelp)
	flag.BoolVar(&insecure, "insecure", false, "use insecure http protocol instead of https.")
	flag.StringVar(&cert, "cert", "cert/cert.pem", "https cert file. need to use https protocol.")
	flag.StringVar(&key, "key", "cert/key.pem", "https key file. need to use https protocol.")
	dbAddrHelp := `host url and port of database.

when ROI_DB_ADDR environment variable is not empty, it will use the value as default.

`
	dbAddrDefault := "localhost:26257"
	dbAddrEnv := os.Getenv("ROI_DB_ADDR")
	if dbAddrEnv != "" {
		dbAddrDefault = dbAddrEnv
		dbAddrHelp += "currently the default value is comming from ROI_DB_ADDR"
	}
	flag.StringVar(&dbAddr, "db-addr", dbAddrDefault, dbAddrHelp)
	flag.StringVar(&dbCA, "db-ca", "db-cert/ca.crt", "root certificate authority file of the database.")
	flag.StringVar(&dbCert, "db-cert", "db-cert/client.root.crt", "client certificate file of database.")
	flag.StringVar(&dbKey, "db-key", "db-cert/client.root.key", "client key file of database.")
	flag.Parse()

	hashFile := "cert/cookie.hash"
	blockFile := "cert/cookie.block"
	blockFileExist, err := anyFileExist(hashFile, blockFile)
	if err != nil {
		log.Fatalf("could not check cookie key file: %s", err)
	}
	if !blockFileExist {
		err = os.MkdirAll(filepath.Dir(hashFile), 0755)
		if err != nil && !os.IsExist(err) {
			log.Fatalf("could not create directory for cookie hash file: %s", err)
		}
		err = os.MkdirAll(filepath.Dir(blockFile), 0755)
		if err != nil && !os.IsExist(err) {
			log.Fatalf("could not create directory for cookie block file: %s", err)
		}
		ioutil.WriteFile(hashFile, securecookie.GenerateRandomKey(64), 0600)
		ioutil.WriteFile(blockFile, securecookie.GenerateRandomKey(32), 0600)
	}

	DB, err = roi.InitDB(dbAddr, dbCA, dbCert, dbKey)
	if err != nil {
		log.Fatalf("could not initialize database: %v", err)
	}

	_, err = roi.GetUser(DB, "admin")
	if err != nil {
		if !errors.As(err, &roi.NotFoundError{}) {
			log.Fatalf("could not check admin user exist: %v", err)
		}
		err := roi.AddUser(DB, "admin", "password1!")
		if err != nil {
			log.Fatalf("could not create admin user: %v", err)
		}
	}

	_, err = roi.GetSite(DB)
	if err != nil {
		if errors.As(err, &roi.NotFoundError{}) {
			err = roi.AddSite(DB)
			if err != nil {
				log.Fatalf("could not create site: %v", err)
			}
		} else {
			log.Fatalf("could not create site: %v", err)
		}
	}

	parseTemplate()

	hashKey, err := ioutil.ReadFile(hashFile)
	if err != nil {
		log.Fatalf("could not read cookie hash key from file '%s'", hashFile)
	}
	blockKey, err := ioutil.ReadFile(blockFile)
	if err != nil {
		log.Fatalf("could not read cookie block key from file '%s'", blockFile)
	}
	cookieHandler = securecookie.New(
		hashKey,
		blockKey,
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/", handle(rootHandler))
	mux.HandleFunc("/login", handle(loginHandler))
	mux.HandleFunc("/logout", handle(logoutHandler))
	mux.HandleFunc("/settings/profile", handle(profileHandler))
	mux.HandleFunc("/update-password", handle(updatePasswordHandler))
	mux.HandleFunc("/signup", handle(signupHandler))
	mux.HandleFunc("/site", handle(siteHandler))
	mux.HandleFunc("/shows", handle(showsHandler))
	mux.HandleFunc("/add-show", handle(addShowHandler))
	mux.HandleFunc("/update-show", handle(updateShowHandler))
	mux.HandleFunc("/units", handle(unitsHandler))
	mux.HandleFunc("/add-group", handle(addGroupHandler))
	mux.HandleFunc("/update-group", handle(updateGroupHandler))
	mux.HandleFunc("/add-unit", handle(addUnitHandler))
	mux.HandleFunc("/update-unit", handle(updateUnitHandler))
	mux.HandleFunc("/update-multi-units", handle(updateMultiUnitsHandler))
	mux.HandleFunc("/update-task", handle(updateTaskHandler))
	mux.HandleFunc("/update-multi-tasks", handle(updateMultiTasksHandler))
	mux.HandleFunc("/review-task", handle(reviewTaskHandler))
	mux.HandleFunc("/add-version", handle(addVersionHandler))
	mux.HandleFunc("/update-version", handle(updateVersionHandler))
	mux.HandleFunc("/review/", handle(reviewHandler))
	mux.HandleFunc("/upload-excel", handle(uploadExcelHandler))
	mux.HandleFunc("/user/", handle(userHandler))
	mux.HandleFunc("/users", handle(usersHandler))
	mux.HandleFunc("/api/v1/show/add", addShowApiHandler)
	mux.HandleFunc("/api/v1/unit/add", addUnitApiHandler)
	mux.HandleFunc("/api/v1/unit/get", getUnitApiHandler)
	mux.HandleFunc("/api/v1/unit-tasks/get", getUnitTasksApiHandler)
	fs := http.FileServer(http.Dir("static"))
	mux.Handle("/static/", http.StripPrefix("/static/", fs))
	thumbfs := http.FileServer(http.Dir("data"))
	mux.Handle("/data/", http.StripPrefix("/data/", thumbfs))

	// Show https binding information
	addrs := strings.Split(addr, ":")

	var protocol string
	site := ""
	defaultHttpPort := "80"
	defaultHttpsPort := "443"
	httpPort := defaultHttpPort
	httpsPort := defaultHttpsPort
	portForwarding := false
	if insecure {
		protocol = "http://"
		site = addrs[0]
		if len(addrs) == 2 {
			httpPort = addrs[1]
		}
	} else {
		protocol = "https://"
		site = addrs[0]
		if len(addrs) == 3 {
			portForwarding = true
			httpPort = addrs[1]
			httpsPort = addrs[2]
		} else if len(addrs) == 2 {
			httpsPort = addrs[1]
		} else if len(addrs) == 1 {
			portForwarding = true
			httpPort = "80"
			httpsPort = "443"
		}
	}
	if site == "" {
		site = "localhost"
	}

	var addrToShow string
	if insecure {
		addrToShow = protocol + site
		if httpPort != defaultHttpPort {
			addrToShow += ":" + httpPort
		}
	} else {
		addrToShow = protocol + site
		if httpsPort != defaultHttpsPort {
			addrToShow += ":" + httpsPort
		}
	}
	fmt.Println()
	log.Printf("roi is start to running. see %s", addrToShow)
	fmt.Println()

	// Bind
	if insecure {
		log.Fatal(http.ListenAndServe(site+":"+httpPort, mux))
	} else {
		if portForwarding {
			log.Printf("http(:%s) request will redirected to https(:%s)", httpPort, httpsPort)
			go func() {
				log.Fatal(http.ListenAndServe(site+":"+httpPort, http.HandlerFunc(redirectToHttps)))
			}()
		}
		log.Fatal(http.ListenAndServeTLS(site+":"+httpsPort, cert, key, mux))
	}
}
