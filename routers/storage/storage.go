package storage

import (
	"fmt"
	clientmodel "github.com/filswan/go-swan-client/model"
	"github.com/filswan/go-swan-client/subcommand"
	libconstants "github.com/filswan/go-swan-lib/constants"
	libutils "github.com/filswan/go-swan-lib/utils"
	"github.com/gin-gonic/gin"
	"net/http"
	"os"
	"path/filepath"
	"payment-bridge/common"
	"payment-bridge/common/errorinfo"
	"payment-bridge/config"
	"payment-bridge/logs"
	"payment-bridge/routers/storage/storageService"
	"strings"
	"time"
)

func SendDealManager(router *gin.RouterGroup) {
	router.POST("/ipfs/upload", UploadFileToIpfs)
	router.GET("/lotus/deal/:task_uuid", SendDeal)
}

func UploadFileToIpfs(c *gin.Context) {
	authorization := c.Request.Header.Get("authorization")
	if len(authorization) == 0 {
		c.JSON(http.StatusOK, common.CreateErrorResponse(errorinfo.NO_AUTHORIZATION_TOKEN_ERROR_CODE, errorinfo.NO_AUTHORIZATION_TOKEN_ERROR_MSG))
		return
	}
	logs.GetLogger().Info(authorization)
	jwtToken := strings.TrimPrefix(authorization, "Bearer ")

	file, err := c.FormFile("file")
	if err != nil {
		logs.GetLogger().Error(err)
		c.JSON(http.StatusOK, common.CreateErrorResponse(errorinfo.HTTP_REQUEST_PARAMS_NULL_ERROR_CODE, errorinfo.HTTP_REQUEST_PARAMS_NULL_ERROR_MSG+":file"))
		return
	}
	taskName := c.PostForm("task_name")

	err = storageService.CreateTask(c, taskName, jwtToken, file)
	if err != nil {
		c.JSON(http.StatusOK, common.CreateErrorResponse(errorinfo.SENDING_DEAL_ERROR_CODE, errorinfo.SENDING_DEAL_ERROR_MSG+":file"))
		return
	}

	c.JSON(http.StatusOK, common.CreateSuccessResponse("succeeded to send deal"))
	return
}

func SendDeal(c *gin.Context) {
	authorization := c.Request.Header.Get("authorization")
	if len(authorization) == 0 {
		c.JSON(http.StatusOK, common.CreateErrorResponse(errorinfo.NO_AUTHORIZATION_TOKEN_ERROR_CODE, errorinfo.NO_AUTHORIZATION_TOKEN_ERROR_MSG))
		return
	}

	logs.GetLogger().Info(authorization)
	jwtToken := strings.TrimPrefix(authorization, "Bearer ")

	task_uuid := c.Param("task_uuid")

	startEpochIntervalHours := config.GetConfig().SwanTask.StartEpochHours
	startEpoch := libutils.GetCurrentEpoch() + (startEpochIntervalHours+1)*libconstants.EPOCH_PER_HOUR

	homedir, err := os.UserHomeDir()
	if err != nil {
		logs.GetLogger().Error(err)
		c.JSON(http.StatusOK, common.CreateErrorResponse(errorinfo.GET_HOME_DIR_ERROR_CODE, errorinfo.GET_HOME_DIR_ERROR_MSG))
		return
	}
	temDirDeal := config.GetConfig().Temp.DirDeal
	temDirDeal = filepath.Join(homedir, temDirDeal[2:])
	err = libutils.CreateDir(temDirDeal)
	if err != nil {
		logs.GetLogger().Error(err)
		c.JSON(http.StatusOK, common.CreateErrorResponse(errorinfo.CREATE_DIR_ERROR_CODE, errorinfo.CREATE_DIR_ERROR_MSG))
		return
	}

	timeStr := time.Now().Format("20060102_150405")
	temDirDeal = filepath.Join(temDirDeal, timeStr)
	carDir := filepath.Join(temDirDeal, "car")
	confDeal := &clientmodel.ConfDeal{
		SwanApiUrl:              config.GetConfig().SwanApi.ApiUrl,
		SwanJwtToken:            jwtToken,
		SenderWallet:            "t3u7pumush376xbytsgs5wabkhtadjzfydxxda2vzyasg7cimkcphswrq66j4dubbhwpnojqd3jie6ermpwvvq",
		VerifiedDeal:            config.GetConfig().SwanTask.VerifiedDeal,
		FastRetrieval:           config.GetConfig().SwanTask.FastRetrieval,
		SkipConfirmation:        true,
		StartEpochIntervalHours: startEpochIntervalHours,
		StartEpoch:              startEpoch,
		OutputDir:               carDir,
	}

	dealSentNum, csvFilePath, carFiles, err := subcommand.SendAutoBidDealByTaskUuid(confDeal, task_uuid)
	if err != nil {
		logs.GetLogger().Error(err)
		c.JSON(http.StatusOK, common.CreateErrorResponse(errorinfo.SENDING_DEAL_ERROR_CODE, errorinfo.SENDING_DEAL_ERROR_MSG))
		return
	}
	fmt.Println(dealSentNum)
	fmt.Println(csvFilePath)
	fmt.Println(carFiles)
	c.JSON(http.StatusOK, common.CreateSuccessResponse("success"))
}
