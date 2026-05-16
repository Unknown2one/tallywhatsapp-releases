package api

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// SendTextMessage sends a text message via WhatsApp and returns the message ID
func SendTextMessage(client *whatsmeow.Client, recipient, message string) (string, error) {
	if !client.IsConnected() {
		return "", fmt.Errorf("not connected to WhatsApp")
	}

	recipientJID, err := parseRecipient(recipient)
	if err != nil {
		return "", err
	}

	msg := &waProto.Message{
		Conversation: proto.String(message),
	}

	resp, err := client.SendMessage(context.Background(), recipientJID, msg)
	if err != nil {
		return "", fmt.Errorf("error sending message: %v", err)
	}

	return resp.ID, nil
}

// SendFileMessage sends a file with optional caption via WhatsApp and returns the message ID
func SendFileMessage(client *whatsmeow.Client, recipient, filePath, caption string) (string, error) {
	if !client.IsConnected() {
		return "", fmt.Errorf("not connected to WhatsApp")
	}

	recipientJID, err := parseRecipient(recipient)
	if err != nil {
		return "", err
	}

	// Read media file
	mediaData, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("error reading media file: %v", err)
	}

	// Determine media type and mime type based on file extension
	fileExt := strings.ToLower(filePath[strings.LastIndex(filePath, ".")+1:])
	mediaType, mimeType := determineMediaType(fileExt)

	// Upload media to WhatsApp servers
	uploadResp, err := client.Upload(context.Background(), mediaData, mediaType)
	if err != nil {
		return "", fmt.Errorf("error uploading media: %v", err)
	}

	// Create the appropriate message type based on media type
	msg := &waProto.Message{}
	switch mediaType {
	case whatsmeow.MediaImage:
		msg.ImageMessage = &waProto.ImageMessage{
			Caption:       proto.String(caption),
			Mimetype:      proto.String(mimeType),
			URL:           &uploadResp.URL,
			DirectPath:    &uploadResp.DirectPath,
			MediaKey:      uploadResp.MediaKey,
			FileEncSHA256: uploadResp.FileEncSHA256,
			FileSHA256:    uploadResp.FileSHA256,
			FileLength:    &uploadResp.FileLength,
		}
	case whatsmeow.MediaVideo:
		msg.VideoMessage = &waProto.VideoMessage{
			Caption:       proto.String(caption),
			Mimetype:      proto.String(mimeType),
			URL:           &uploadResp.URL,
			DirectPath:    &uploadResp.DirectPath,
			MediaKey:      uploadResp.MediaKey,
			FileEncSHA256: uploadResp.FileEncSHA256,
			FileSHA256:    uploadResp.FileSHA256,
			FileLength:    &uploadResp.FileLength,
		}
	case whatsmeow.MediaDocument:
		fileName := filePath[strings.LastIndex(filePath, "\\")+1:]
		msg.DocumentMessage = &waProto.DocumentMessage{
			Title:         proto.String(fileName),
			Caption:       proto.String(caption),
			Mimetype:      proto.String(mimeType),
			URL:           &uploadResp.URL,
			DirectPath:    &uploadResp.DirectPath,
			MediaKey:      uploadResp.MediaKey,
			FileEncSHA256: uploadResp.FileEncSHA256,
			FileSHA256:    uploadResp.FileSHA256,
			FileLength:    &uploadResp.FileLength,
		}
	default:
		return "", fmt.Errorf("unsupported media type: %v", mediaType)
	}

	resp, err := client.SendMessage(context.Background(), recipientJID, msg)
	if err != nil {
		return "", fmt.Errorf("error sending file: %v", err)
	}

	return resp.ID, nil
}

// parseRecipient converts a phone number or JID string to a types.JID
func parseRecipient(recipient string) (types.JID, error) {
	var recipientJID types.JID
	var err error

	// Check if recipient is already a JID
	if strings.Contains(recipient, "@") {
		recipientJID, err = types.ParseJID(recipient)
		if err != nil {
			return types.JID{}, fmt.Errorf("error parsing JID: %v", err)
		}
	} else {
		// Create JID from phone number
		recipientJID = types.JID{
			User:   recipient,
			Server: "s.whatsapp.net",
		}
	}

	return recipientJID, nil
}

// determineMediaType returns the WhatsApp media type and mime type for a file extension
func determineMediaType(fileExt string) (whatsmeow.MediaType, string) {
	switch fileExt {
	// Image types
	case "jpg", "jpeg":
		return whatsmeow.MediaImage, "image/jpeg"
	case "png":
		return whatsmeow.MediaImage, "image/png"
	case "gif":
		return whatsmeow.MediaImage, "image/gif"
	case "webp":
		return whatsmeow.MediaImage, "image/webp"

	// Video types
	case "mp4":
		return whatsmeow.MediaVideo, "video/mp4"
	case "avi":
		return whatsmeow.MediaVideo, "video/avi"
	case "mov":
		return whatsmeow.MediaVideo, "video/quicktime"

	// Document types with proper MIME types
	case "pdf":
		return whatsmeow.MediaDocument, "application/pdf"
	case "doc":
		return whatsmeow.MediaDocument, "application/msword"
	case "docx":
		return whatsmeow.MediaDocument, "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case "xls":
		return whatsmeow.MediaDocument, "application/vnd.ms-excel"
	case "xlsx":
		return whatsmeow.MediaDocument, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case "ppt":
		return whatsmeow.MediaDocument, "application/vnd.ms-powerpoint"
	case "pptx":
		return whatsmeow.MediaDocument, "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case "txt":
		return whatsmeow.MediaDocument, "text/plain"
	case "csv":
		return whatsmeow.MediaDocument, "text/csv"
	case "zip":
		return whatsmeow.MediaDocument, "application/zip"

	// Fallback for unknown types
	default:
		return whatsmeow.MediaDocument, "application/octet-stream"
	}
}
