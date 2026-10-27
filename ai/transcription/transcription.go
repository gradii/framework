package transcription

import (
	frameworkai "github.com/gradii/framework/ai"
	contractsai "github.com/gradii/framework/contracts/ai"
	contractsfilesystem "github.com/gradii/framework/contracts/filesystem"
)

func WithMimeType(mimeType string) contractsai.AttachmentOption {
	return frameworkai.WithMimeType(mimeType)
}

func WithDisk(disk string) contractsai.AttachmentOption {
	return frameworkai.WithDisk(disk)
}

func WithTitle(title string) contractsai.AttachmentOption {
	return frameworkai.WithTitle(title)
}

func FromPath(path string, options ...contractsai.AttachmentOption) contractsai.Attachment {
	return frameworkai.DocumentFromPath(path, options...)
}

func FromStorage(path string, options ...contractsai.AttachmentOption) contractsai.Attachment {
	return frameworkai.DocumentFromStorage(path, options...)
}

func FromUpload(file contractsfilesystem.File, options ...contractsai.AttachmentOption) contractsai.Attachment {
	return frameworkai.DocumentFromUpload(file, options...)
}
