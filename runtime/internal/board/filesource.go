package board

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// loadFileSource 读文件目录数据源（只取类型信息，不做任何读文件动作）。
func loadFileSource(vault, name string) (map[string]any, error) {
	b, err := os.ReadFile(filepath.Join(filepath.Dir(vault), "datasources", name+".json"))
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}
