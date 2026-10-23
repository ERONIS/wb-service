package transfer_service

import "github.com/ERONIS/wb-service/internal/feature/transfer/service/workflow"

type CreateTransfer = workflow.CreateTransfer
type Repository = workflow.Repository
type InitializationGroup = workflow.InitializationGroup
type InitializationItem = workflow.InitializationItem
type InitializeTransferCommand = workflow.InitializeTransferCommand

const MaxPreparingPageSize = workflow.MaxPreparingPageSize

var ErrInitializationInvalid = workflow.ErrInitializationInvalid
var ErrInitializationConflict = workflow.ErrInitializationConflict
