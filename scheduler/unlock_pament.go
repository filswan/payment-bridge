package scheduler

import (
	"fmt"
	"payment-bridge/common/constants"
	"payment-bridge/common/utils"
	"payment-bridge/config"
	"payment-bridge/models"
	"payment-bridge/on-chain/client"
	"payment-bridge/on-chain/goBind"
	"strconv"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/filswan/go-swan-lib/logs"
	"github.com/robfig/cron"
)

func CreateUnlockScheduler() {
	Mutex := &sync.Mutex{}
	c := cron.New()
	err := c.AddFunc(config.GetConfig().ScheduleRule.UnlockPaymentRule, func() {
		logs.GetLogger().Info("^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^ create task  scheduler is running at " + time.Now().Format("2006-01-02 15:04:05"))
		Mutex.Lock()
		err := UnlockPayment()
		Mutex.Unlock()
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

func UnlockPayment() error {
	offlineDeals, err := models.GetOfflineDeals2BeUnlocked()
	if err != nil {
		logs.GetLogger().Error(err)
		return err
	}

	if len(offlineDeals) == 0 {
		logs.GetLogger().Info("no deal to be unlocked")
		return nil
	}

	ethClient, err := client.GetEthClient()
	if err != nil {
		logs.GetLogger().Error(err)
		return err
	}

	recipient, swanPaymentTransactor, err := client.GetSwanPaymentTransactor(ethClient)
	if err != nil {
		logs.GetLogger().Error(err)
		return err
	}

	filswanOracleSession, err := client.GetFilswanOracleSession(ethClient)
	if err != nil {
		logs.GetLogger().Error(err)
		return err
	}

	tansactOpts, err := client.GetTransactOpts(ethClient)
	if err != nil {
		logs.GetLogger().Error(err)
		return err
	}

	for _, offlineDeal := range offlineDeals {
		unlocked, err := unlockDeal(filswanOracleSession, offlineDeal, ethClient, swanPaymentTransactor, tansactOpts, *recipient)
		if err != nil {
			logs.GetLogger().Error(err)
			continue
		}

		if !unlocked {
			logs.GetLogger().Info(fmt.Sprintf("deal:%d, not unlocked", offlineDeal.Id))
			continue
		}

		err = refund(offlineDeal, swanPaymentTransactor, tansactOpts)
		if err != nil {
			logs.GetLogger().Error(err)
			continue
		}
	}
	return nil
}

func unlockDeal(filswanOracleSession *goBind.FilswanOracleSession, offlineDeal *models.OfflineDeal, ethClient *ethclient.Client, swanPaymentTransactor *goBind.SwanPaymentTransactor, tansactOpts *bind.TransactOpts, recipient common.Address) (bool, error) {
	dealIdStr := strconv.FormatInt(offlineDeal.DealId, 10)
	isPaymentAvailable, err := filswanOracleSession.IsCarPaymentAvailable(dealIdStr, recipient)
	if err != nil {
		logs.GetLogger().Error(err)
		return false, err
	}

	if !isPaymentAvailable {
		logs.GetLogger().Info(fmt.Sprintf("payment is not available for deal:%s,recipient:%s", dealIdStr, recipient))
		return false, nil
	}

	tx, err := swanPaymentTransactor.UnlockCarPayment(tansactOpts, dealIdStr, recipient)
	if err != nil {
		logs.GetLogger().Error(err)
		return false, err
	}

	txReceipt, err := utils.CheckTx(ethClient, tx)
	if err != nil {
		logs.GetLogger().Error(err)
		return false, err
	}

	if txReceipt.Status != uint64(1) {
		err := fmt.Errorf("unlock failed! txHash=" + tx.Hash().Hex())
		logs.GetLogger().Error(err)
		return false, err
	}

	unlockTxStatus := constants.TRANSACTION_STATUS_SUCCESS
	logs.GetLogger().Println("unlock success! txHash=" + tx.Hash().Hex())

	err = models.UpdateOfflineDealUnlockStatus(offlineDeal.Id, constants.OFFLINE_DEAL_UNLOCK_STATUS_UNLOCKED)
	if err != nil {
		logs.GetLogger().Error(err)
		return false, err
	}

	if len(txReceipt.Logs) == 0 {
		return true, nil
	}

	err = client.SaveEventUnlockPayment(txReceipt.Logs, unlockTxStatus, offlineDeal.DealId)
	if err != nil {
		logs.GetLogger().Error(err)
		return false, err
	}

	return true, nil
}

func refund(offlineDeal *models.OfflineDeal, swanPaymentTransactor *goBind.SwanPaymentTransactor, tansactOpts *bind.TransactOpts) error {
	offlineDealsNotUnlocked, err := models.GetOfflineDealsNotUnlockedByDealFileId(offlineDeal.DealFileId)
	if err != nil {
		logs.GetLogger().Error(err)
		return err
	}

	if len(offlineDealsNotUnlocked) > 0 {
		logs.GetLogger().Info(fmt.Sprintf("still has deal not unlocked, unable to refund for deal:%d", offlineDeal.DealId))
		return nil
	}

	var srcFilePayloadCids []string
	srcFiles, err := models.GetSourceFilesByDealFileId(offlineDeal.DealFileId)
	if err != nil {
		logs.GetLogger().Error(err)
		return err
	}

	for _, srcFile := range srcFiles {
		srcFilePayloadCids = append(srcFilePayloadCids, srcFile.PayloadCid)
	}

	lockPaymentStatus := constants.LOCK_PAYMENT_STATUS_UNLOCK_REFUNDED
	_, err = swanPaymentTransactor.Refund(tansactOpts, srcFilePayloadCids)
	if err != nil {
		lockPaymentStatus = constants.LOCK_PAYMENT_STATUS_UNLOCK_REFUNDFAILED
		logs.GetLogger().Error(err)
	}

	currrentTime := utils.GetCurrentUtcMilliSecond()
	err = models.UpdateDealFile(models.DealFile{ID: offlineDeal.DealFileId},
		map[string]interface{}{"lock_payment_status": lockPaymentStatus, "update_at": currrentTime})
	if err != nil {
		logs.GetLogger().Error(err)
	}

	return nil
}
