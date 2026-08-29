//go:build windows

package main

import (
	"errors"
	"path/filepath"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsTreeWatcher struct {
	handle windows.Handle
	done   chan struct{}
	exited chan struct{}
	once   sync.Once
}

func startTreeWatcher(root string, _ []string, onChange func(string), onFailure func(error)) (treeWatcher, error) {
	path, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		path,
		windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return nil, err
	}
	watcher := &windowsTreeWatcher{handle: handle, done: make(chan struct{}), exited: make(chan struct{})}
	go watcher.run(filepath.Clean(root), onChange, onFailure)
	return watcher, nil
}

func (watcher *windowsTreeWatcher) run(root string, onChange func(string), onFailure func(error)) {
	defer close(watcher.exited)
	const notifyMask = windows.FILE_NOTIFY_CHANGE_FILE_NAME |
		windows.FILE_NOTIFY_CHANGE_DIR_NAME |
		windows.FILE_NOTIFY_CHANGE_ATTRIBUTES |
		windows.FILE_NOTIFY_CHANGE_SIZE |
		windows.FILE_NOTIFY_CHANGE_LAST_WRITE |
		windows.FILE_NOTIFY_CHANGE_CREATION
	buffer := make([]byte, 64*1024)
	for {
		select {
		case <-watcher.done:
			return
		default:
		}
		var length uint32
		err := windows.ReadDirectoryChanges(watcher.handle, &buffer[0], uint32(len(buffer)), true, notifyMask, &length, nil, 0)
		if err != nil {
			select {
			case <-watcher.done:
				return
			default:
			}
			if !errors.Is(err, windows.ERROR_OPERATION_ABORTED) {
				onFailure(err)
			}
			return
		}
		if length == 0 {
			onFailure(errors.New("filesystem notification buffer overflow"))
			return
		}
		for offset := uint32(0); offset < length; {
			information := (*windows.FileNotifyInformation)(unsafe.Pointer(&buffer[offset]))
			nameLength := int(information.FileNameLength / 2)
			name := windows.UTF16ToString(unsafe.Slice(&information.FileName, nameLength))
			onChange(filepath.Join(root, name))
			if information.NextEntryOffset == 0 {
				break
			}
			offset += information.NextEntryOffset
		}
	}
}

func (watcher *windowsTreeWatcher) Close() error {
	var err error
	watcher.once.Do(func() {
		close(watcher.done)
		cancelErr := windows.CancelIoEx(watcher.handle, nil)
		if cancelErr != nil && !errors.Is(cancelErr, windows.ERROR_NOT_FOUND) {
			err = cancelErr
		}
		<-watcher.exited
		if closeErr := windows.CloseHandle(watcher.handle); err == nil {
			err = closeErr
		}
	})
	return err
}
