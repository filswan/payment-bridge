package scheduler

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"payment-bridge/common/constants"
	"payment-bridge/common/utils"
	"payment-bridge/config"
	"payment-bridge/database"
	"payment-bridge/models"
	"strconv"
	"time"

	"github.com/filswan/go-swan-client/command"
	"github.com/filswan/go-swan-lib/logs"
	libmodel "github.com/filswan/go-swan-lib/model"
	libutils "github.com/filswan/go-swan-lib/utils"
	"github.com/robfig/cron"
)

const (
	SRC_FILE_SIZE_MIN = int64(1 * 1024) // * 1024 // * 1024
	CAR_FILE_SIZE_MIN = int64(1 * 1024) // * 1024 //* 1024
	DURATION          = 500
)

var srcDirs []SrcDirInfo

type SrcDirInfo struct {
	SrcDir   string
	SrcFiles []SrcFileInfo
}

type SrcFileInfo struct {
	Id         int64
	PayloadCid string
	VrfRandStr string
	Filepath   string
	FileUrl    string
}

func GetSrcDir() (*SrcDirInfo, error) {
	for _, srcDir := range srcDirs {
		filesSize, err := getFilesSize(srcDir.SrcDir)
		if err != nil {
			return nil, err
		}

		if *filesSize < SRC_FILE_SIZE_MIN {
			return &srcDir, nil
		}
	}

	tempDirDeal := config.GetConfig().SwanTask.DirDeal
	homedir, err := os.UserHomeDir()
	if err != nil {
		logs.GetLogger().Error(err)
		return nil, err
	}
	if len(tempDirDeal) < 2 {
		err := fmt.Errorf("deal directory config error, please contact administrator")
		logs.GetLogger().Error(err)
		return nil, err
	}

	tempDirDeal = filepath.Join(homedir, tempDirDeal[2:])

	currentTime, err := time.Now().UTC().MarshalText()
	if err != nil {
		logs.GetLogger().Error(err)
		return nil, err
	}

	tempDirDeal = filepath.Join(tempDirDeal, string(currentTime))

	srcDirPath := filepath.Join(tempDirDeal, "src")
	srcDir := SrcDirInfo{
		SrcDir: srcDirPath,
	}

	srcDirs = append(srcDirs, srcDir)

	return &srcDir, nil
}

func CreateCarScheduler() {
	c := cron.New()
	err := c.AddFunc(config.GetConfig().ScheduleRule.CreateCarRule, func() {
		logs.GetLogger().Info("creating car file scheduler is running at " + time.Now().Format("2006-01-02 15:04:05"))
		createCar()
	})
	if err != nil {
		logs.GetLogger().Error(err)
		return
	}
	c.Start()
}

func getFilesSize(dir string) (*int64, error) {
	srcFiles, err := ioutil.ReadDir(dir)
	if err != nil {
		logs.GetLogger().Error(err)
		return nil, err
	}
	srcFilesSize := int64(0)
	for _, srcFile := range srcFiles {
		srcFilesSize = srcFilesSize + srcFile.Size()
	}

	return &srcFilesSize, nil
}

func createCar() {
	for _, srcDir := range srcDirs {
		srcFilesSize, err := getFilesSize(srcDir.SrcDir)
		if err != nil {
			logs.GetLogger().Error(err)
			return
		}

		if *srcFilesSize < SRC_FILE_SIZE_MIN {
			return
		}

		fileDesc, err := createCarFile(srcDir.SrcDir)
		if err != nil {
			logs.GetLogger().Error(err)
			return
		}

		err = saveCarInfo2DB(fileDesc, srcDir)
		if err != nil {
			logs.GetLogger().Error(err)
			return
		}
	}
}

func createCarFile(srcDir string) (*libmodel.FileDesc, error) {
	srcFilesSize, err := getFilesSize(srcDir)
	if err != nil {
		logs.GetLogger().Error(err)
		return nil, err
	}

	if *srcFilesSize < SRC_FILE_SIZE_MIN {
		err := fmt.Errorf("source file size is less than %d", SRC_FILE_SIZE_MIN)
		logs.GetLogger().Error(err)
		return nil, err
	}

	temDirDeal := filepath.Dir(srcDir)

	carDir := filepath.Join(temDirDeal, "car")
	err = libutils.CreateDir(carDir)
	if err != nil {
		logs.GetLogger().Error(err)
		return nil, err
	}
	cmdIpfsCar := &command.CmdIpfsCar{
		LotusClientApiUrl:         config.GetConfig().Lotus.ClientApiUrl,
		LotusClientAccessToken:    config.GetConfig().Lotus.ClientAccessToken,
		InputDir:                  srcDir,
		OutputDir:                 carDir,
		GenerateMd5:               false,
		IpfsServerUploadUrlPrefix: config.GetConfig().IpfsServer.UploadUrlPrefix,
	}
	fileDescs, err := cmdIpfsCar.CreateIpfsCarFiles()
	if err != nil {
		logs.GetLogger().Error(err)
		return nil, err
	}

	fileDesc := fileDescs[0]

	if fileDesc.CarFileSize < CAR_FILE_SIZE_MIN {
		err := fmt.Errorf("car file size is less than %d", CAR_FILE_SIZE_MIN)
		logs.GetLogger().Error(err)
		return nil, err
	}

	logs.GetLogger().Info("car files created in ", carDir, "payload_cid=", fileDesc.PayloadCid)

	return fileDesc, nil
}

func saveCarInfo2DB(fileDesc *libmodel.FileDesc, srcDir SrcDirInfo) error {
	db := database.GetDBTransaction()
	currentTime := utils.GetEpochInMillis()
	dealFile := new(models.DealFile)
	dealFile.CarFileName = fileDesc.CarFileName
	dealFile.CarFilePath = fileDesc.CarFilePath
	dealFile.CarFileSize = fileDesc.CarFileSize
	dealFile.CarMd5 = fileDesc.CarFileMd5
	dealFile.PayloadCid = fileDesc.PayloadCid
	dealFile.PieceCid = fileDesc.PieceCid
	dealFile.DealCid = fileDesc.PayloadCid
	dealFile.CreateAt = strconv.FormatInt(currentTime, 10)
	dealFile.UpdateAt = strconv.FormatInt(currentTime, 10)
	dealFile.Duration = DURATION
	dealFile.LockPaymentStatus = constants.LOCK_PAYMENT_STATUS_WAITING
	dealFile.IsDeleted = utils.GetBoolPointer(false)
	err := database.SaveOneInTransaction(db, dealFile)
	if err != nil {
		db.Rollback()
		logs.GetLogger().Error(err)
		return err
	}

	for _, srcFile := range srcDir.SrcFiles {
		filepMap := new(models.SourceFileDealFileMap)
		filepMap.SourceFileId = srcFile.Id
		filepMap.DealFileId = dealFile.ID
		filepMap.FileIndex = 0
		filepMap.CreateAt = strconv.FormatInt(currentTime, 10)
		filepMap.UpdateAt = strconv.FormatInt(currentTime, 10)
		err = database.SaveOne(filepMap)
		if err != nil {
			db.Rollback()
			logs.GetLogger().Error(err)
			return err
		}
	}

	err = db.Commit().Error
	if err != nil {
		logs.GetLogger().Error(err)
		return err
	}

	return nil
}