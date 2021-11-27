package scheduler

import (
	"encoding/json"
	clientmodel "github.com/filswan/go-swan-client/model"
	"github.com/filswan/go-swan-client/subcommand"
	libconstants "github.com/filswan/go-swan-lib/constants"
	libutils "github.com/filswan/go-swan-lib/utils"
	"github.com/robfig/cron"
	"net/http"
	"os"
	"path/filepath"
	"payment-bridge/common/constants"
	"payment-bridge/common/httpClient"
	"payment-bridge/config"
	"payment-bridge/database"
	"payment-bridge/logs"
	"payment-bridge/models"
	"time"
)

func SendDealScheduler() {
	c := cron.New()
	err := c.AddFunc(config.GetConfig().ScheduleRule.SendDealRule, func() {
		logs.GetLogger().Println("^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^ send deal scheduler is running at " + time.Now().Format("2006-01-02 15:04:05"))
		err := DoSendDealScheduler()
		if err != nil {
			logs.GetLogger().Error(err)
			return
		}
	})
	if err != nil {
		logs.GetLogger().Error(err)
		return
	}
	c.Start()
}
func DoSendDealScheduler() error {
	dealList, err := GetTaskListShouldBeSendDealFromLocal()
	if err != nil {
		logs.GetLogger().Error(err)
		return err
	}
	for _, v := range dealList {
		taskInfo, err := GetTaskStatusByUuid(v.TaskUuid)
		if err != nil {
			logs.GetLogger().Error(err)
			continue
		}
		if taskInfo.Data.Task.Status == constants.TASK_STATUS_ASSIGNED {
			logs.GetLogger().Println("################################## start to send deal ##################################")
			logs.GetLogger().Println(" task uuid : ", v.TaskUuid)
			v.SendDealStatus = constants.SEND_DEAL_STATUS_SUCCESS
			err = sendDeal(v.TaskUuid, v)
			if err != nil {
				logs.GetLogger().Error(err)
				v.SendDealStatus = constants.SEND_DEAL_STATUS_FAIL
				err = database.SaveOne(v)
				if err != nil {
					logs.GetLogger().Error(err)
					continue
				}
				continue
			}
			logs.GetLogger().Println("################################## end to send deal ##################################")
			err = database.SaveOne(v)
			if err != nil {
				logs.GetLogger().Error(err)
				continue
			}
		}
	}
	return nil
}

func GetTaskListShouldBeSendDealFromLocal() ([]*models.DealFile, error) {
	whereCondition := "send_deal_status ='' and lower(lock_payment_status)=lower('" + constants.LOCK_PAYMENT_STATUS_SUCCESS + "') and task_uuid != '' "
	dealList, err := models.FindDealFileList(whereCondition, "create_at desc", "10", "0")
	if err != nil {
		logs.GetLogger().Error(err)
		return nil, err
	}
	return dealList, nil
}

func GetTaskListShouldBeSigServiceFromSwan() (*models.OfflineDealResult, error) {
	url := config.GetConfig().SwanApi.ApiUrl + config.GetConfig().SwanApi.GetShouldSendTaskUrlSuffix
	response, err := httpClient.SendRequestAndGetBytes(http.MethodGet, url, nil, nil)
	if err != nil {
		logs.GetLogger().Error(err)
		return nil, err
	}
	var results *models.OfflineDealResult
	err = json.Unmarshal(response, &results)
	if err != nil {
		logs.GetLogger().Error(err)
		return nil, err
	}
	return results, nil
}

func sendDeal(taskUuid string, file *models.DealFile) error {
	startEpochIntervalHours := config.GetConfig().SwanTask.StartEpochHours
	startEpoch := libutils.GetCurrentEpoch() + (startEpochIntervalHours+1)*libconstants.EPOCH_PER_HOUR

	homedir, err := os.UserHomeDir()
	if err != nil {
		logs.GetLogger().Error(err)
		return err
	}
	temDirDeal := config.GetConfig().SwanTask.DirDeal
	temDirDeal = filepath.Join(homedir, temDirDeal[2:])
	err = libutils.CreateDir(temDirDeal)
	if err != nil {
		logs.GetLogger().Error(err)
		return err
	}

	timeStr := time.Now().Format("20060102_150405")
	temDirDeal = filepath.Join(temDirDeal, timeStr)
	carDir := filepath.Join(temDirDeal, "car")
	confDeal := &clientmodel.ConfDeal{
		SwanApiUrl:                   config.GetConfig().SwanApi.ApiUrl,
		SwanApiKey:                   config.GetConfig().SwanApi.ApiKey,
		SwanAccessToken:              config.GetConfig().SwanApi.AccessToken,
		SenderWallet:                 config.GetConfig().FileCoinWallet,
		VerifiedDeal:                 config.GetConfig().SwanTask.VerifiedDeal,
		FastRetrieval:                config.GetConfig().SwanTask.FastRetrieval,
		SkipConfirmation:             true,
		StartEpochIntervalHours:      startEpochIntervalHours,
		StartEpoch:                   startEpoch,
		OutputDir:                    carDir,
		LotusClientApiUrl:            config.GetConfig().Lotus.ApiUrl,
		LotusClientAccessToken:       config.GetConfig().Lotus.AccessToken,
		Duration:                     file.Duration,
		RelativeEpochFromMainNetwork: config.GetConfig().SwanTask.RelativeEpochFromMainNetwork,
	}
	confDeal.DealSourceIds = append(confDeal.DealSourceIds, libconstants.TASK_SOURCE_ID_SWAN_PAYMENT)

	dealSentNum, csvFilePath, carFiles, err := subcommand.SendAutoBidDealsByTaskUuid(confDeal, taskUuid)
	if err != nil {
		logs.GetLogger().Error(err)
		return err
	}
	logs.GetLogger().Info("------------------------------send deal success---------------------------------")
	logs.GetLogger().Info("dealSentNum = ", dealSentNum)
	logs.GetLogger().Info("csvFilePath = ", csvFilePath)
	logs.GetLogger().Info("carFiles = ", carFiles)
	return nil
}

func checkIfHaveLockPayment(payloadCid string) (bool, error) {
	polygonEventList, err := models.FindEventLockPayment(&models.EventLockPayment{PayloadCid: payloadCid}, "id desc", "", "0")
	if err != nil {
		logs.GetLogger().Error(err)
		return false, err
	}
	if len(polygonEventList) > 0 {
		return true, nil
	} else {
		return false, nil
	}
}

func GetTaskStatusByUuid(taskUuid string) (*AutoGenerated, error) {
	url := config.GetConfig().SwanApi.ApiUrl + "/tasks/" + taskUuid
	response, err := httpClient.SendRequestAndGetBytes(http.MethodGet, url, nil, nil)
	if err != nil {
		logs.GetLogger().Error(err)
		return nil, err
	}

	var taskInfo *AutoGenerated
	err = json.Unmarshal(response, &taskInfo)
	if err != nil {
		logs.GetLogger().Error(err)
		return nil, err
	}
	return taskInfo, nil
}

type AutoGenerated struct {
	Data struct {
		AverageBid string        `json:"average_bid"`
		Bid        []interface{} `json:"bid"`
		BidCount   int           `json:"bid_count"`
		Deal       []struct {
			ContractID    string      `json:"contract_id"`
			Cost          interface{} `json:"cost"`
			CreatedAt     string      `json:"created_at"`
			DealCid       interface{} `json:"deal_cid"`
			FileName      string      `json:"file_name"`
			FilePath      interface{} `json:"file_path"`
			FileSize      string      `json:"file_size"`
			FileSourceURL string      `json:"file_source_url"`
			ID            int         `json:"id"`
			Md5Origin     string      `json:"md5_origin"`
			MinerID       interface{} `json:"miner_id"`
			Note          interface{} `json:"note"`
			PayloadCid    string      `json:"payload_cid"`
			PieceCid      string      `json:"piece_cid"`
			PinStatus     string      `json:"pin_status"`
			StartEpoch    int         `json:"start_epoch"`
			Status        string      `json:"status"`
			TaskID        int         `json:"task_id"`
			UpdatedAt     string      `json:"updated_at"`
			UserID        int         `json:"user_id"`
		} `json:"deal"`
		DealCompleteRate string      `json:"deal_complete_rate"`
		Miner            interface{} `json:"miner"`
		Poster           struct {
			AvatarURL         string      `json:"avatar_url"`
			CompleteTaskCount int         `json:"complete_task_count"`
			ContactInfo       interface{} `json:"contact_info"`
			MemberSince       string      `json:"member_since"`
		} `json:"poster"`
		Task struct {
			BidMode        int         `json:"bid_mode"`
			CreatedOn      string      `json:"created_on"`
			CuratedDataset interface{} `json:"curated_dataset"`
			Description    interface{} `json:"description"`
			Duration       int         `json:"duration"`
			ExpireDays     int         `json:"expire_days"`
			FastRetrieval  int         `json:"fast_retrieval"`
			IsPublic       int         `json:"is_public"`
			MaxPrice       string      `json:"max_price"`
			MinPrice       interface{} `json:"min_price"`
			MinerID        interface{} `json:"miner_id"`
			SourceID       int         `json:"source_id"`
			Status         string      `json:"status"`
			Tags           interface{} `json:"tags"`
			TaskFileName   string      `json:"task_file_name"`
			TaskID         int         `json:"task_id"`
			TaskName       string      `json:"task_name"`
			Type           string      `json:"type"`
			UpdatedOn      string      `json:"updated_on"`
			UUID           string      `json:"uuid"`
		} `json:"task"`
		TotalDealCount int `json:"total_deal_count"`
		TotalItems     int `json:"total_items"`
	} `json:"data"`
	Status string `json:"status"`
}
