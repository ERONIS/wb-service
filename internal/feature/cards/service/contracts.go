package cards_service


type ImportFile struct {
	OriginalFilename string
	TelegramFileID   string
	MIMEType         string
	Size             int64
}
