package actions

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/bitrise-io/go-utils/log"
	"github.com/bitrise-io/go-utils/sliceutil"
	"github.com/bitrise-io/addons-firebase-testlab/analyticsutils"
	"github.com/bitrise-io/addons-firebase-testlab/database"
	"github.com/bitrise-io/addons-firebase-testlab/firebaseutils"
	"github.com/bitrise-io/addons-firebase-testlab/models"
	"github.com/bitrise-io/addons-firebase-testlab/renderers"
	"github.com/gobuffalo/buffalo"
	"github.com/markbates/pop/nulls"
	"github.com/pkg/errors"
	testing "google.golang.org/api/testing/v1"
)

// TestGet ...
func TestGet(c buffalo.Context) error {
	buildSlug := c.Param("build_slug")
	appSlug := c.Param("app_slug")

	build, err := database.GetBuild(appSlug, buildSlug)
	if err != nil {
		log.Errorf("[!] Exception: Failed to get build from DB, error: %s", err)
		return c.Render(http.StatusInternalServerError, r.String("Invalid request"))
	}

	fAPI, err := firebaseutils.New()
	if err != nil {
		log.Errorf("[!] Exception: Failed to create Firebase API model, error: %s", err)
		return c.Render(http.StatusInternalServerError, r.String("Invalid request"))
	}

	if build.TestHistoryID == "" || build.TestExecutionID == "" {
		matrix, err := fAPI.GetHistoryAndExecutionIDByMatrixID(build.TestMatrixID)
		if err != nil {
			matrix, err = fAPI.GetHistoryAndExecutionIDByMatrixID(build.TestMatrixID)
			if err != nil {
				log.Errorf("[!] Exception: failed to get test matrix(%s) in build: %s, app: %s, error: %s \nwith retry...", build.TestMatrixID, build.AppSlug, build.BuildSlug, err)
				return c.Render(http.StatusInternalServerError, r.String("Failed to get test status"))
			}
		}

		if isMessageAnError(matrix.State) {
			return c.Render(http.StatusInternalServerError, r.String("Failed to get test status: %s(%s)", matrix.State, matrix.InvalidMatrixDetails))
		}

		if len(matrix.TestExecutions) == 0 {
			build.LastRequest = nulls.NewTime(time.Now())

			err = database.UpdateBuild(build)
			if err != nil {
				log.Errorf("[!] Exception: Failed to update last request timestamp: %+v", build)
				return c.Render(http.StatusInternalServerError, r.String("Failed to get test status"))
			}
			return c.Render(http.StatusOK, r.JSON(map[string]string{"state": matrix.State}))
		}

		if matrix.TestExecutions[0].ToolResultsStep == nil {
			build.LastRequest = nulls.NewTime(time.Now())

			err = database.UpdateBuild(build)
			if err != nil {
				log.Errorf("[!] Exception: Failed to update last request timestamp: %+v", build)
				return c.Render(http.StatusInternalServerError, r.String("Failed to get test status"))
			}
			return c.Render(http.StatusOK, r.JSON(map[string]string{"state": matrix.State}))
		}

		build.TestHistoryID = matrix.TestExecutions[0].ToolResultsStep.HistoryId
		build.TestExecutionID = matrix.TestExecutions[0].ToolResultsStep.ExecutionId
	}

	steps, err := fAPI.GetTestsByHistoryAndExecutionID(build.TestHistoryID, build.TestExecutionID, "steps(state,name,outcome,dimensionValue,testExecutionStep)")
	if err != nil {
		steps, err = fAPI.GetTestsByHistoryAndExecutionID(build.TestHistoryID, build.TestExecutionID, "steps(state,name,outcome,dimensionValue,testExecutionStep)")
		if err != nil {
			log.Errorf("[!] Exception: failed to get test by HistoryID(%s) and ExecutionID(%s) matrix:(%s) in build: %s, app: %s, error: %s. \nwith retry failed...", build.TestHistoryID, build.TestExecutionID, build.TestMatrixID, build.AppSlug, build.BuildSlug, err)
			return c.Render(http.StatusInternalServerError, r.String("Failed to get test status"))
		}
	}

	if len(steps.Steps) > 0 {
		isIOS := false

		completed := true
		for _, step := range steps.Steps {
			if step.State != "complete" {
				completed = false
			}
			if strings.Contains(strings.ToLower(step.Name), "ios") {
				isIOS = true
			}
		}
		if build.BuildSessionEnabled && completed {
			build.BuildSessionEnabled = false

			testType := "instrumentation"

			if !strings.Contains(strings.ToLower(steps.Steps[0].Name), "instrumentation") {
				testType = "robo"
			}

			result := "success"
			for _, step := range steps.Steps {
				if step.Outcome.Summary != "success" {
					result = "failed"
				}

				if !isIOS {
					device := &testing.AndroidDevice{}
					for _, dim := range step.DimensionValue {
						if dim != nil {
							switch dim.Key {
							case "Model":
								device.AndroidModelId = dim.Value
							case "Version":
								device.AndroidVersionId = dim.Value
							case "Locale":
								device.Locale = dim.Value
							case "Orientation":
								device.Orientation = dim.Value
							}
						}
					}
					analyticsutils.SendTestingEventDevices(analyticsutils.EventTestingTestFinishedOnDevice,
						appSlug,
						buildSlug,
						testType,
						[]*testing.AndroidDevice{device},
						map[string]interface{}{
							"test_result": step.Outcome.Summary,
						})
				} else {
					device := &testing.IosDevice{}
					for _, dim := range step.DimensionValue {
						if dim != nil {
							switch dim.Key {
							case "Model":
								device.IosModelId = dim.Value
							case "Version":
								device.IosVersionId = dim.Value
							case "Locale":
								device.Locale = dim.Value
							case "Orientation":
								device.Orientation = dim.Value
							}
						}
					}
					analyticsutils.SendIOSTestingEventDevices(analyticsutils.EventIOSTestingTestFinishedOnDevice,
						appSlug,
						buildSlug,
						"",
						[]*testing.IosDevice{device},
						map[string]interface{}{
							"test_result": step.Outcome.Summary,
						})
				}
			}
			if !isIOS {
				analyticsutils.SendTestingEvent(analyticsutils.EventTestingTestFinished,
					appSlug,
					buildSlug,
					testType,
					map[string]interface{}{
						"test_result": result,
					})
			} else {
				analyticsutils.SendTestingEvent(analyticsutils.EventIOSTestingTestFinished,
					appSlug,
					buildSlug,
					"",
					map[string]interface{}{
						"test_result": result,
					})
			}
		}
	}

	build.LastRequest = nulls.NewTime(time.Now())

	err = database.UpdateBuild(build)
	if err != nil {
		log.Errorf("[!] Exception: Failed to update last request timestamp: %+v", build)
		return c.Render(http.StatusInternalServerError, r.String("Failed to get test status"))
	}

	return c.Render(http.StatusOK, r.JSON(steps))
}

// TestPost ...
func TestPost(c buffalo.Context) error {
	buildSlug := c.Param("build_slug")
	appSlug := c.Param("app_slug")

	build, err := database.GetBuild(appSlug, buildSlug)
	if err != nil {
		log.Errorf("[!] Exception: Failed to get build from DB, error: %s", err)
		return c.Render(http.StatusInternalServerError, r.String("Internal error"))
	}

	if build.TestMatrixID != "" {
		return c.Render(http.StatusForbidden, r.JSON(map[string]string{"error": "A Test Matrix has already been started for this build."}))
	}

	postTestrequestModel := &testing.TestMatrix{}
	if err := json.NewDecoder(c.Request().Body).Decode(postTestrequestModel); err != nil {
		log.Errorf("Failed to decode request body, error: %s", err)
		return c.Render(http.StatusInternalServerError, r.String("Invalid request"))
	}

	if postTestrequestModel.EnvironmentMatrix.AndroidDeviceList != nil {
		if err := firebaseutils.ValidateAndroidDevice(postTestrequestModel.EnvironmentMatrix.AndroidDeviceList.AndroidDevices); err != nil {
			return c.Render(http.StatusNotAcceptable, r.String("Invalid device configuration: %s", err))
		}
	}

	fAPI, err := firebaseutils.New()
	if err != nil {
		log.Errorf("[!] Exception: Failed to create Firebase API model, error: %s", err)
		return c.Render(http.StatusInternalServerError, r.String("Invalid request"))
	}

	startResp, err := fAPI.StartTestMatrix(appSlug, buildSlug, postTestrequestModel)
	if err != nil {
		log.Errorf("[!] Exception: Failed to start Test Matrix, error: %s", err)
		return c.Render(http.StatusInternalServerError, r.String("%s", err))
	}

	build.TestMatrixID = startResp.TestMatrixId

	startTime, err := time.Parse("2006-01-02T15:04:05.999Z", startResp.Timestamp)
	if err != nil {
		log.Errorf("[!] Exception: Failed to parse startTime, error: %s", err)
		return c.Render(http.StatusInternalServerError, r.JSON(map[string]string{"error": "Internal error"}))
	}

	build.TestStartTime = nulls.NewTime(startTime)
	build.TestEndTime = nulls.NewTime(startTime)
	build.LastRequest = nulls.NewTime(time.Now())

	err = database.UpdateBuild(build)
	if err != nil {
		log.Errorf("[!] Exception: Failed to update DB, error: %s", err)
		return c.Render(http.StatusInternalServerError, r.JSON(map[string]string{"error": "Internal error"}))
	}

	if postTestrequestModel.TestSpecification.IosXcTest == nil {
		testType := "robo"
		if postTestrequestModel.TestSpecification.AndroidInstrumentationTest != nil {
			testType = "instrumentation"
		}

		analyticsutils.SendTestingEvent(analyticsutils.EventTestingTestStarted,
			appSlug,
			buildSlug,
			testType,
			nil)
		analyticsutils.SendTestingEventDevices(analyticsutils.EventTestingTestStartedOnDevice,
			appSlug,
			buildSlug,
			testType,
			postTestrequestModel.EnvironmentMatrix.AndroidDeviceList.AndroidDevices,
			nil)
	} else {
		analyticsutils.SendTestingEvent(analyticsutils.EventIOSTestingTestStarted,
			appSlug,
			buildSlug,
			"",
			nil)
		analyticsutils.SendIOSTestingEventDevices(analyticsutils.EventIOSTestingTestStartedOnDevice,
			appSlug,
			buildSlug,
			"",
			postTestrequestModel.EnvironmentMatrix.IosDeviceList.IosDevices,
			nil)
	}

	return c.Render(http.StatusOK, r.String(""))
}

// TestAssetsGet ...
func TestAssetsGet(c buffalo.Context) error {
	buildSlug := c.Param("build_slug")

	fAPI, err := firebaseutils.New()
	if err != nil {
		log.Errorf("Failed to create Firebase API model, error: %s", err)
		return c.Render(http.StatusInternalServerError, r.String("Invalid request"))
	}

	downloadUrlsModel, err := fAPI.DownloadTestAssets(buildSlug)
	if err != nil {
		log.Errorf("Failed to get asset download urls, error: %s", err)
		return c.Render(http.StatusInternalServerError, r.String("Invalid request"))
	}

	return c.Render(http.StatusOK, renderers.JSON(downloadUrlsModel))
}

// TestAssetsPost ...
func TestAssetsPost(c buffalo.Context) error {
	buildSlug := c.Param("build_slug")
	appSlug := c.Param("app_slug")

	buildExists, err := database.IsBuildExists(appSlug, buildSlug)
	if err != nil {
		log.Errorf("Failed to check if build exists")
		return c.Render(http.StatusInternalServerError, r.JSON(map[string]string{"error": "Internal error"}))
	}

	if buildExists {
		log.Errorf("Build already exists")
		return c.Render(http.StatusForbidden, r.JSON(map[string]string{"error": "Build already exists"}))
	}

	fAPI, err := firebaseutils.New()
	if err != nil {
		log.Errorf("Failed to create Firebase API model, error: %s", err)
		return c.Render(http.StatusInternalServerError, r.String("Invalid request"))
	}

	resp, err := fAPI.UploadTestAssets(buildSlug)
	if err != nil {
		log.Errorf("Failed to get upload urls, error: %s", err)
		return c.Render(http.StatusInternalServerError, r.String("Invalid request"))
	}

	err = database.AddBuild(&models.Build{BuildSlug: buildSlug, AppSlug: appSlug, LastRequest: nulls.NewTime(time.Now()), BuildSessionEnabled: true})
	if err != nil {
		log.Errorf("Failed to save build, error: %+v", errors.WithStack(err))
		return c.Render(http.StatusInternalServerError, r.JSON(map[string]string{"error": "Invalid request"}))
	}

	analyticsutils.SendUploadEvent(analyticsutils.EventUploadFileUploadRequested,
		appSlug,
		buildSlug)

	return c.Render(http.StatusOK, r.JSON(resp))
}

func isMessageAnError(message string) bool {
	errorMessages := []string{
		//"TEST_STATE_UNSPECIFIED",
		//"VALIDATING",
		//"PENDING",
		//"RUNNING",
		//"FINISHED",
		"ERROR",
		"UNSUPPORTED_ENVIRONMENT",
		"INCOMPATIBLE_ENVIRONMENT",
		"INCOMPATIBLE_ARCHITECTURE",
		"CANCELLED",
		"INVALID",
	}
	return sliceutil.IsStringInSlice(message, errorMessages)
}
