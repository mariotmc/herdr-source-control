package git

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mariotmc/herdr-source-control/internal/domain"
)

type statusResult struct {
	branch         domain.BranchState
	statusUpstream string
	statusAhead    uint64
	statusBehind   uint64
	statusCounts   bool
	changes        []domain.Change
}

func parsePorcelain(data []byte) (statusResult, error) {
	var parsed statusResult
	if len(data) == 0 {
		return parsed, nil
	}
	if data[len(data)-1] != 0 {
		return statusResult{}, errors.New("unterminated porcelain record")
	}
	fields := bytes.Split(data[:len(data)-1], []byte{0})
	for i := 0; i < len(fields); i++ {
		record := fields[i]
		if len(record) == 0 {
			return statusResult{}, errors.New("empty porcelain record")
		}
		switch record[0] {
		case '#':
			if err := parseHeader(record, &parsed); err != nil {
				return statusResult{}, err
			}
		case '1':
			change, err := parseOrdinary(record)
			if err != nil {
				return statusResult{}, err
			}
			parsed.changes = append(parsed.changes, change)
		case '2':
			if i+1 >= len(fields) || len(fields[i+1]) == 0 {
				return statusResult{}, errors.New("rename missing original path")
			}
			change, err := parseRename(record, fields[i+1])
			if err != nil {
				return statusResult{}, err
			}
			parsed.changes = append(parsed.changes, change)
			i++
		case 'u':
			change, err := parseUnmerged(record)
			if err != nil {
				return statusResult{}, err
			}
			parsed.changes = append(parsed.changes, change)
		case '?':
			if len(record) < 3 || record[1] != ' ' {
				return statusResult{}, errors.New("malformed untracked record")
			}
			parsed.changes = append(parsed.changes, domain.Change{Path: bytes.Clone(record[2:]), Untracked: true})
		case '!':
			if len(record) < 3 || record[1] != ' ' {
				return statusResult{}, errors.New("malformed ignored record")
			}
		default:
			return statusResult{}, fmt.Errorf("unknown porcelain record %q", record[0])
		}
	}
	return parsed, nil
}

func parseHeader(record []byte, parsed *statusResult) error {
	parts := bytes.SplitN(record, []byte(" "), 3)
	if len(parts) != 3 || string(parts[0]) != "#" {
		return errors.New("malformed branch header")
	}
	key, value := string(parts[1]), parts[2]
	switch key {
	case "branch.oid":
		if string(value) == "(initial)" {
			parsed.branch.State = domain.HeadUnborn
			parsed.branch.OID = ""
		} else {
			parsed.branch.OID = string(value)
		}
	case "branch.head":
		if string(value) == "(detached)" {
			parsed.branch.State = domain.HeadDetached
			parsed.branch.Name = ""
		} else {
			parsed.branch.Name = string(value)
			if parsed.branch.State != domain.HeadUnborn {
				parsed.branch.State = domain.HeadAttached
			}
		}
	case "branch.upstream":
		parsed.statusUpstream = string(value)
	case "branch.ab":
		parts := bytes.Fields(value)
		if len(parts) != 2 || len(parts[0]) < 2 || parts[0][0] != '+' || len(parts[1]) < 2 || parts[1][0] != '-' {
			return errors.New("malformed ahead/behind header")
		}
		ahead, err := strconv.ParseUint(string(parts[0][1:]), 10, 64)
		if err != nil {
			return fmt.Errorf("ahead count: %w", err)
		}
		behind, err := strconv.ParseUint(string(parts[1][1:]), 10, 64)
		if err != nil {
			return fmt.Errorf("behind count: %w", err)
		}
		parsed.statusAhead, parsed.statusBehind, parsed.statusCounts = ahead, behind, true
	default:
		return fmt.Errorf("unknown branch header %q", key)
	}
	return nil
}

func parseOrdinary(record []byte) (domain.Change, error) {
	parts := bytes.SplitN(record, []byte(" "), 9)
	if len(parts) != 9 || string(parts[0]) != "1" || len(parts[1]) != 2 || len(parts[2]) == 0 || len(parts[8]) == 0 {
		return domain.Change{}, errors.New("malformed ordinary record")
	}
	return makeChange(parts[1], parts[2], parts[8]), nil
}

func parseRename(record, original []byte) (domain.Change, error) {
	parts := bytes.SplitN(record, []byte(" "), 10)
	if len(parts) != 10 || string(parts[0]) != "2" || len(parts[1]) != 2 || len(parts[2]) == 0 || len(parts[8]) < 2 || len(parts[9]) == 0 {
		return domain.Change{}, errors.New("malformed rename record")
	}
	if parts[8][0] != 'R' && parts[8][0] != 'C' {
		return domain.Change{}, errors.New("invalid rename score kind")
	}
	score, err := strconv.Atoi(string(parts[8][1:]))
	if err != nil || score < 0 || score > 100 {
		return domain.Change{}, errors.New("invalid rename score")
	}
	change := makeChange(parts[1], parts[2], parts[9])
	change.OriginalPath = bytes.Clone(original)
	change.Score = score
	return change, nil
}

func parseUnmerged(record []byte) (domain.Change, error) {
	parts := bytes.SplitN(record, []byte(" "), 11)
	if len(parts) != 11 || string(parts[0]) != "u" || len(parts[1]) != 2 || len(parts[2]) == 0 || len(parts[10]) == 0 {
		return domain.Change{}, errors.New("malformed unmerged record")
	}
	change := makeChange(parts[1], parts[2], parts[10])
	change.Conflicted = true
	change.IndexStatus = domain.StatusUnmerged
	change.WorktreeStatus = domain.StatusUnmerged
	return change, nil
}

func makeChange(xy, submodule, path []byte) domain.Change {
	return domain.Change{
		Path: bytes.Clone(path), IndexStatus: statusFromCode(xy[0]), WorktreeStatus: statusFromCode(xy[1]),
		RawXY: [2]byte{xy[0], xy[1]}, Submodule: string(submodule),
	}
}

func statusFromCode(code byte) domain.Status {
	switch code {
	case '.':
		return domain.StatusUnmodified
	case 'M':
		return domain.StatusModified
	case 'A':
		return domain.StatusAdded
	case 'D':
		return domain.StatusDeleted
	case 'R':
		return domain.StatusRenamed
	case 'C':
		return domain.StatusCopied
	case 'T':
		return domain.StatusTypeChanged
	case 'U':
		return domain.StatusUnmerged
	default:
		return domain.StatusUnknown
	}
}

func parseCount(data []byte) (uint64, uint64, error) {
	parts := strings.Fields(string(removeOneLineEnding(data)))
	if len(parts) != 2 {
		return 0, 0, errors.New("malformed rev-list count")
	}
	ahead, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	behind, err := strconv.ParseUint(parts[1], 10, 64)
	return ahead, behind, err
}
