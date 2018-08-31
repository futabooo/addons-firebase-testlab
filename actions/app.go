package actions

import (
	"fmt"
	"net/http"
	"os"
	"time"

	testing "google.golang.org/api/testing/v1"

	"github.com/bitrise-io/go-utils/log"
	"github.com/gobuffalo/buffalo"
	"github.com/gobuffalo/buffalo/render"
	"github.com/markbates/pop/nulls"
	"github.com/pkg/errors"

	"github.com/bitrise-io/addons-firebase-testlab-android/analyticsutils"
	"github.com/bitrise-io/addons-firebase-testlab-android/database"
	"github.com/bitrise-io/addons-firebase-testlab-android/firebaseutils"
	"github.com/bitrise-io/addons-firebase-testlab-android/models"
	"github.com/bitrise-io/addons-firebase-testlab-android/tasks"

	"github.com/bitrise-io/addons-firebase-testlab-android/configs"
)

const (
	contextUserUsage                    = "user_usage"
	contextUserAllUsages                = "user_usages"
	contextMatrixHistoryAndExecutionIDs = "matrix_history_and_execution_ids"
)

var app *buffalo.App
var r *render.Engine

func initApp() error {
	err := configs.Setup()
	if err != nil {
		return fmt.Errorf("Failed to init configs, error: %+v", err)
	}

	err = database.InitDB()
	if err != nil {
		return fmt.Errorf("Failed to init DB, error: %s", err)
	}

	err = analyticsutils.Init()
	if err != nil {
		log.Warnf("%s", err)
	}

	fAPI, err := firebaseutils.New()
	if err != nil {
		return fmt.Errorf("Failed to create Firebase API model, error: %s", err)
	}

	// init devices catalog
	firebaseutils.DevicesCatalog, err = fAPI.GetDeviceCatalog()
	if err != nil {
		return fmt.Errorf("Failed to get devices catalog, error: %s", err)
	}

	return nil
}

// App ...
func App() *buffalo.App {
	if app == nil {
		r = render.New(render.Options{TemplateEngine: render.GoTemplateEngine})

		if err := initApp(); err != nil {
			fmt.Printf("[!] Exception: Failed to init app, error: %+v", err)
		}

		app = buffalo.Automatic(buffalo.Options{
			Env:         configs.GetENV(),
			SessionName: "_addons-firebase-testlab-android_session",
		})

		if len(os.Args) > 1 {
			switch os.Args[1] {
			case "clean-builds":
				err := tasks.CleanBuilds()
				if err != nil {
					log.Errorf("Failed to run task: clean-builds, error: %s", err)
					os.Exit(1)
				}
			case "builds-count":
				err := tasks.GetBuildsCount()
				if err != nil {
					log.Errorf("Failed to run task: builds-count, error: %s", err)
					os.Exit(1)
				}
			case "add-backend-user":
				//
				// DEVELOPMENT
				if configs.GetENV() == "development" {
					prefillAppSlug := os.Getenv("DEV_PREFILL_APPSLUG")
					prefillBuildSlug := os.Getenv("DEV_PREFILL_BUILDSLUG")
					buildExists, err := database.IsBuildExists(prefillAppSlug, prefillBuildSlug)
					if err != nil {
						log.Errorf("[DEV] Failed to get build from DB, error: %s", err)
					}
					if !buildExists {
						err = database.AddBuild(&models.Build{TestMatrixID: "matrix-1lpev7dwzfs6l", TestHistoryID: "bh.ac1fd45f1d1a66a5", TestExecutionID: "4928682413877019352", BuildSlug: prefillBuildSlug, AppSlug: prefillAppSlug, LastRequest: nulls.NewTime(time.Now()), BuildSessionEnabled: true})
						if err != nil {
							log.Errorf("[DEV] Failed to save build, error: %+v", errors.WithStack(err))
						}
					}
				}
			case "add-test-user":
				if len(os.Args) != 3 {
					log.Errorf("No appSlug specified")
					os.Exit(1)
				}
				err := tasks.AddDefaultUser(os.Args[2])
				if err != nil {
					log.Errorf("Failed to run task: builds-count, error: %s", err)
					os.Exit(1)
				}
			case "test-devices":
				device := &testing.AndroidDevice{
					AndroidModelId:   "Nexus4",
					AndroidVersionId: "21",
				}
				log.Infof("Checking device: %s with version: %s", device.AndroidModelId, device.AndroidVersionId)
				if err := firebaseutils.ValidateAndroidDevice([]*testing.AndroidDevice{device}); err != nil {
					log.Errorf("Device: %s should be available with version: %s, error: %s", device.AndroidModelId, device.AndroidVersionId, err)
				} else {
					log.Donef("OK")
				}
				device = &testing.AndroidDevice{
					AndroidModelId:   "Nexus45",
					AndroidVersionId: "21",
				}
				log.Infof("Checking device: %s with version: %s", device.AndroidModelId, device.AndroidVersionId)
				if err := firebaseutils.ValidateAndroidDevice([]*testing.AndroidDevice{device}); err == nil {
					log.Errorf("Device: %s shouldn't be available with version: %s, error: %s", device.AndroidModelId, device.AndroidVersionId, err)
				} else {
					log.Donef("OK - %s", err)
				}
				device = &testing.AndroidDevice{
					AndroidModelId:   "Nexus4",
					AndroidVersionId: "10",
				}
				log.Infof("Checking device: %s with version: %s", device.AndroidModelId, device.AndroidVersionId)
				if err := firebaseutils.ValidateAndroidDevice([]*testing.AndroidDevice{device}); err == nil {
					log.Errorf("Device: %s shouldn't be available with version: %s, error: %s", device.AndroidModelId, device.AndroidVersionId, err)
				} else {
					log.Donef("OK - %s", err)
				}
				device = &testing.AndroidDevice{
					AndroidModelId:   "hammerhead",
					AndroidVersionId: "21",
				}
				log.Infof("Checking device: %s with version: %s", device.AndroidModelId, device.AndroidVersionId)
				if err := firebaseutils.ValidateAndroidDevice([]*testing.AndroidDevice{device}); err == nil {
					log.Errorf("Device: %s shouldn't be available with version: %s, error: %s", device.AndroidModelId, device.AndroidVersionId, err)
				} else {
					log.Donef("OK - %s", err)
				}
			}
			os.Exit(0)
		}

		//
		// ENDPOINTS

		//
		// ROOT
		app.GET("/", RootGetHandler)

		//
		// PROVISIONING
		provision := app.Group("/provision")
		provision.Use(authenticateWithAccessToken)              // only accessible with access token
		provision.POST("/", ProvisionPostHandler)               // set new provision
		provision.PUT("/{app_slug}", ProvisionPutHandler)       // update provision
		provision.DELETE("/{app_slug}", ProvisionDeleteHandler) // delete provision

		//
		// TESTING
		test := app.Group("/test") // main addon test functionality
		test.Use(authenticateRequestWithToken)
		test.GET("/{app_slug}/{build_slug}/{token}", authorizeForBuild(TestGet))                                                       // get test matrix data
		test.POST("/{app_slug}/{build_slug}/{token}", authorizeForRunningBuildViaBitriseAPI(authorizeForBuild(TestPost)))              // start test matrix
		test.POST("/assets/{app_slug}/{build_slug}/{token}", authorizeForRunningBuildViaBitriseAPI(TestAssetsPost)) // get signed upload urls for assets
		test.GET("/assets/{app_slug}/{build_slug}/{token}", authorizeForBuild(TestAssetsGet))                                          // get signed download urls for assets

		//
		// API
		api := app.Group("/api")                                           // api group
		api.Use(validateUserLoginStatus)                                   // check if signature is valid
		api.GET("/builds/{build_slug}", DashboardAPIGetHandler)            // return dashboard resources
		api.GET("/builds/{build_slug}/steps/{step_id}", StepAPIGetHandler) // return step resources

		//
		// DASHBOARD
		//app.Use(validateUserLoginStatus)                                          // check if signature is valid - this is not required
		app.Use(serveSVGs)                                                        // serve svgs content in the template
		app.GET("/builds/{build_slug}", DashboardIndexGetHandler)                 // dashboard index page
		app.GET("/builds/{build_slug}/steps/{step_id}", DashboardIndexGetHandler) // dashboard index page
		app.GET("/templates/dashboard", DashboardGetHandler)                      // dashboard main page
		app.GET("/templates/details", DashboardDetailsGetHandler)                 // dashboard details page
		app.POST("/login", DashboardLoginPostHandler)                             // sso login handler
		app.ServeFiles("/assets", http.Dir("./frontend/assets/compiled"))         // serve assets for dashboard
	}

	return app
}