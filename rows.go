// Copyright 2016 - 2020 The excelize Authors. All rights reserved. Use of
// this source code is governed by a BSD-style license that can be found in
// the LICENSE file.
//
// Package excelize providing a set of functions that allow you to write to
// and read from XLSX files. Support reads and writes XLSX file generated by
// Microsoft Excel™ 2007 and later. Support save file without losing original
// charts of XLSX. This library needs Go version 1.10 or later.

package excelize

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"strconv"
)

// GetRows return all the rows in a sheet by given worksheet name (case
// sensitive). For example:
//
//    rows, err := f.Rows("Sheet1")
//    if err != nil {
//        fmt.Println(err)
//        return
//    }
//    for rows.Next() {
//        row, err := rows.Columns()
//        if err != nil {
//            fmt.Println(err)
//        }
//        for _, colCell := range row {
//            fmt.Print(colCell, "\t")
//        }
//        fmt.Println()
//    }
//
func (f *File) GetRows(sheet string) ([][]string, error) {
	rows, err := f.Rows(sheet)
	if err != nil {
		return nil, err
	}
	results := make([][]string, 0, 64)
	for rows.Next() {
		if rows.Error() != nil {
			break
		}
		row, err := rows.Columns()
		if err != nil {
			break
		}
		results = append(results, row)
	}
	return results, nil
}

// Rows defines an iterator to a sheet
type Rows struct {
	err                        error
	curRow, totalRow, stashRow int
	sheet                      string
	rows                       []xlsxRow
	f                          *File
	decoder                    *xml.Decoder
}

// Next will return true if find the next row element.
func (rows *Rows) Next() bool {
	rows.curRow++
	return rows.curRow <= rows.totalRow
}

// Error will return the error when the find next row element
func (rows *Rows) Error() error {
	return rows.err
}

// Columns return the current row's column values
func (rows *Rows) Columns() ([]string, error) {
	var (
		err          error
		inElement    string
		row, cellCol int
		columns      []string
	)

	if rows.stashRow >= rows.curRow {
		return columns, err
	}

	d := rows.f.sharedStringsReader()
	for {
		token, _ := rows.decoder.Token()
		if token == nil {
			break
		}
		switch startElement := token.(type) {
		case xml.StartElement:
			inElement = startElement.Name.Local
			if inElement == "row" {
				for _, attr := range startElement.Attr {
					if attr.Name.Local == "r" {
						row, err = strconv.Atoi(attr.Value)
						if err != nil {
							return columns, err
						}
						if row > rows.curRow {
							rows.stashRow = row - 1
							return columns, err
						}
					}
				}
			}
			if inElement == "c" {
				colCell := xlsxC{}
				_ = rows.decoder.DecodeElement(&colCell, &startElement)
				cellCol, _, err = CellNameToCoordinates(colCell.R)
				if err != nil {
					return columns, err
				}
				blank := cellCol - len(columns)
				for i := 1; i < blank; i++ {
					columns = append(columns, "")
				}
				val, _ := colCell.getValueFrom(rows.f, d)
				columns = append(columns, val)
			}
		case xml.EndElement:
			inElement = startElement.Name.Local
			if inElement == "row" {
				return columns, err
			}
		}
	}
	return columns, err
}

// ErrSheetNotExist defines an error of sheet is not exist
type ErrSheetNotExist struct {
	SheetName string
}

func (err ErrSheetNotExist) Error() string {
	return fmt.Sprintf("sheet %s is not exist", string(err.SheetName))
}

// Rows return a rows iterator. For example:
//
//    rows, err := f.Rows("Sheet1")
//    if err != nil {
//        fmt.Println(err)
//        return
//    }
//    for rows.Next() {
//        row, err := rows.Columns()
//        if err != nil {
//            fmt.Println(err)
//        }
//        for _, colCell := range row {
//            fmt.Print(colCell, "\t")
//        }
//        fmt.Println()
//    }
//
func (f *File) Rows(sheet string) (*Rows, error) {
	name, ok := f.sheetMap[trimSheetName(sheet)]
	if !ok {
		return nil, ErrSheetNotExist{sheet}
	}
	if f.Sheet[name] != nil {
		// flush data
		output, _ := xml.Marshal(f.Sheet[name])
		f.saveFileList(name, replaceRelationshipsNameSpaceBytes(output))
	}
	var (
		err       error
		inElement string
		row       int
		rows      Rows
	)
	decoder := f.xmlNewDecoder(bytes.NewReader(f.readXML(name)))
	for {
		token, _ := decoder.Token()
		if token == nil {
			break
		}
		switch startElement := token.(type) {
		case xml.StartElement:
			inElement = startElement.Name.Local
			if inElement == "row" {
				for _, attr := range startElement.Attr {
					if attr.Name.Local == "r" {
						row, err = strconv.Atoi(attr.Value)
						if err != nil {
							return &rows, err
						}
					}
				}
				rows.totalRow = row
			}
		default:
		}
	}
	rows.f = f
	rows.sheet = name
	rows.decoder = f.xmlNewDecoder(bytes.NewReader(f.readXML(name)))
	return &rows, nil
}

// SetRowHeight provides a function to set the height of a single row. For
// example, set the height of the first row in Sheet1:
//
//    err := f.SetRowHeight("Sheet1", 1, 50)
//
func (f *File) SetRowHeight(sheet string, row int, height float64) error {
	if row < 1 {
		return newInvalidRowNumberError(row)
	}

	xlsx, err := f.workSheetReader(sheet)
	if err != nil {
		return err
	}

	prepareSheetXML(xlsx, 0, row)

	rowIdx := row - 1
	xlsx.SheetData.Row[rowIdx].Ht = height
	xlsx.SheetData.Row[rowIdx].CustomHeight = true
	return nil
}

// getRowHeight provides a function to get row height in pixels by given sheet
// name and row index.
func (f *File) getRowHeight(sheet string, row int) int {
	xlsx, _ := f.workSheetReader(sheet)
	for i := range xlsx.SheetData.Row {
		v := &xlsx.SheetData.Row[i]
		if v.R == row+1 && v.Ht != 0 {
			return int(convertRowHeightToPixels(v.Ht))
		}
	}
	// Optimisation for when the row heights haven't changed.
	return int(defaultRowHeightPixels)
}

// GetRowHeight provides a function to get row height by given worksheet name
// and row index. For example, get the height of the first row in Sheet1:
//
//    height, err := f.GetRowHeight("Sheet1", 1)
//
func (f *File) GetRowHeight(sheet string, row int) (float64, error) {
	if row < 1 {
		return defaultRowHeightPixels, newInvalidRowNumberError(row)
	}

	xlsx, err := f.workSheetReader(sheet)
	if err != nil {
		return defaultRowHeightPixels, err
	}
	if row > len(xlsx.SheetData.Row) {
		return defaultRowHeightPixels, nil // it will be better to use 0, but we take care with BC
	}
	for _, v := range xlsx.SheetData.Row {
		if v.R == row && v.Ht != 0 {
			return v.Ht, nil
		}
	}
	// Optimisation for when the row heights haven't changed.
	return defaultRowHeightPixels, nil
}

// sharedStringsReader provides a function to get the pointer to the structure
// after deserialization of xl/sharedStrings.xml.
func (f *File) sharedStringsReader() *xlsxSST {
	var err error

	if f.SharedStrings == nil {
		var sharedStrings xlsxSST
		ss := f.readXML("xl/sharedStrings.xml")
		if len(ss) == 0 {
			ss = f.readXML("xl/SharedStrings.xml")
		}
		if err = f.xmlNewDecoder(bytes.NewReader(namespaceStrictToTransitional(ss))).
			Decode(&sharedStrings); err != nil && err != io.EOF {
			log.Printf("xml decode error: %s", err)
		}
		f.SharedStrings = &sharedStrings
	}

	return f.SharedStrings
}

// getValueFrom return a value from a column/row cell, this function is
// inteded to be used with for range on rows an argument with the xlsx opened
// file.
func (xlsx *xlsxC) getValueFrom(f *File, d *xlsxSST) (string, error) {
	switch xlsx.T {
	case "s":
		if xlsx.V != "" {
			xlsxSI := 0
			xlsxSI, _ = strconv.Atoi(xlsx.V)
			if len(d.SI) > xlsxSI {
				return f.formattedValue(xlsx.S, d.SI[xlsxSI].String()), nil
			}
		}
		return f.formattedValue(xlsx.S, xlsx.V), nil
	case "str":
		return f.formattedValue(xlsx.S, xlsx.V), nil
	case "inlineStr":
		if xlsx.IS != nil {
			return f.formattedValue(xlsx.S, xlsx.IS.String()), nil
		}
		return f.formattedValue(xlsx.S, xlsx.V), nil
	default:
		return f.formattedValue(xlsx.S, xlsx.V), nil
	}
}

// SetRowVisible provides a function to set visible of a single row by given
// worksheet name and Excel row number. For example, hide row 2 in Sheet1:
//
//    err := f.SetRowVisible("Sheet1", 2, false)
//
func (f *File) SetRowVisible(sheet string, row int, visible bool) error {
	if row < 1 {
		return newInvalidRowNumberError(row)
	}

	xlsx, err := f.workSheetReader(sheet)
	if err != nil {
		return err
	}
	prepareSheetXML(xlsx, 0, row)
	xlsx.SheetData.Row[row-1].Hidden = !visible
	return nil
}

// GetRowVisible provides a function to get visible of a single row by given
// worksheet name and Excel row number. For example, get visible state of row
// 2 in Sheet1:
//
//    visible, err := f.GetRowVisible("Sheet1", 2)
//
func (f *File) GetRowVisible(sheet string, row int) (bool, error) {
	if row < 1 {
		return false, newInvalidRowNumberError(row)
	}

	xlsx, err := f.workSheetReader(sheet)
	if err != nil {
		return false, err
	}
	if row > len(xlsx.SheetData.Row) {
		return false, nil
	}
	return !xlsx.SheetData.Row[row-1].Hidden, nil
}

// SetRowOutlineLevel provides a function to set outline level number of a
// single row by given worksheet name and Excel row number. The value of
// parameter 'level' is 1-7. For example, outline row 2 in Sheet1 to level 1:
//
//    err := f.SetRowOutlineLevel("Sheet1", 2, 1)
//
func (f *File) SetRowOutlineLevel(sheet string, row int, level uint8) error {
	if row < 1 {
		return newInvalidRowNumberError(row)
	}
	if level > 7 || level < 1 {
		return errors.New("invalid outline level")
	}
	xlsx, err := f.workSheetReader(sheet)
	if err != nil {
		return err
	}
	prepareSheetXML(xlsx, 0, row)
	xlsx.SheetData.Row[row-1].OutlineLevel = level
	return nil
}

// GetRowOutlineLevel provides a function to get outline level number of a
// single row by given worksheet name and Excel row number. For example, get
// outline number of row 2 in Sheet1:
//
//    level, err := f.GetRowOutlineLevel("Sheet1", 2)
//
func (f *File) GetRowOutlineLevel(sheet string, row int) (uint8, error) {
	if row < 1 {
		return 0, newInvalidRowNumberError(row)
	}
	xlsx, err := f.workSheetReader(sheet)
	if err != nil {
		return 0, err
	}
	if row > len(xlsx.SheetData.Row) {
		return 0, nil
	}
	return xlsx.SheetData.Row[row-1].OutlineLevel, nil
}

// RemoveRow provides a function to remove single row by given worksheet name
// and Excel row number. For example, remove row 3 in Sheet1:
//
//    err := f.RemoveRow("Sheet1", 3)
//
// Use this method with caution, which will affect changes in references such
// as formulas, charts, and so on. If there is any referenced value of the
// worksheet, it will cause a file error when you open it. The excelize only
// partially updates these references currently.
func (f *File) RemoveRow(sheet string, row int) error {
	if row < 1 {
		return newInvalidRowNumberError(row)
	}

	xlsx, err := f.workSheetReader(sheet)
	if err != nil {
		return err
	}
	if row > len(xlsx.SheetData.Row) {
		return f.adjustHelper(sheet, rows, row, -1)
	}
	for rowIdx := range xlsx.SheetData.Row {
		if xlsx.SheetData.Row[rowIdx].R == row {
			xlsx.SheetData.Row = append(xlsx.SheetData.Row[:rowIdx],
				xlsx.SheetData.Row[rowIdx+1:]...)[:len(xlsx.SheetData.Row)-1]
			return f.adjustHelper(sheet, rows, row, -1)
		}
	}
	return nil
}

// InsertRow provides a function to insert a new row after given Excel row
// number starting from 1. For example, create a new row before row 3 in
// Sheet1:
//
//    err := f.InsertRow("Sheet1", 3)
//
// Use this method with caution, which will affect changes in references such
// as formulas, charts, and so on. If there is any referenced value of the
// worksheet, it will cause a file error when you open it. The excelize only
// partially updates these references currently.
func (f *File) InsertRow(sheet string, row int) error {
	if row < 1 {
		return newInvalidRowNumberError(row)
	}
	return f.adjustHelper(sheet, rows, row, 1)
}

// DuplicateRow inserts a copy of specified row (by its Excel row number) below
//
//    err := f.DuplicateRow("Sheet1", 2)
//
// Use this method with caution, which will affect changes in references such
// as formulas, charts, and so on. If there is any referenced value of the
// worksheet, it will cause a file error when you open it. The excelize only
// partially updates these references currently.
func (f *File) DuplicateRow(sheet string, row int) error {
	return f.DuplicateRowTo(sheet, row, row+1)
}

// DuplicateRowTo inserts a copy of specified row by it Excel number
// to specified row position moving down exists rows after target position
//
//    err := f.DuplicateRowTo("Sheet1", 2, 7)
//
// Use this method with caution, which will affect changes in references such
// as formulas, charts, and so on. If there is any referenced value of the
// worksheet, it will cause a file error when you open it. The excelize only
// partially updates these references currently.
func (f *File) DuplicateRowTo(sheet string, row, row2 int) error {
	if row < 1 {
		return newInvalidRowNumberError(row)
	}

	xlsx, err := f.workSheetReader(sheet)
	if err != nil {
		return err
	}
	if row > len(xlsx.SheetData.Row) || row2 < 1 || row == row2 {
		return nil
	}

	var ok bool
	var rowCopy xlsxRow

	for i, r := range xlsx.SheetData.Row {
		if r.R == row {
			rowCopy = xlsx.SheetData.Row[i]
			ok = true
			break
		}
	}
	if !ok {
		return nil
	}

	if err := f.adjustHelper(sheet, rows, row2, 1); err != nil {
		return err
	}

	idx2 := -1
	for i, r := range xlsx.SheetData.Row {
		if r.R == row2 {
			idx2 = i
			break
		}
	}
	if idx2 == -1 && len(xlsx.SheetData.Row) >= row2 {
		return nil
	}

	rowCopy.C = append(make([]xlsxC, 0, len(rowCopy.C)), rowCopy.C...)
	f.ajustSingleRowDimensions(&rowCopy, row2)

	if idx2 != -1 {
		xlsx.SheetData.Row[idx2] = rowCopy
	} else {
		xlsx.SheetData.Row = append(xlsx.SheetData.Row, rowCopy)
	}
	return f.duplicateMergeCells(sheet, xlsx, row, row2)
}

// duplicateMergeCells merge cells in the destination row if there are single
// row merged cells in the copied row.
func (f *File) duplicateMergeCells(sheet string, xlsx *xlsxWorksheet, row, row2 int) error {
	if xlsx.MergeCells == nil {
		return nil
	}
	if row > row2 {
		row++
	}
	for _, rng := range xlsx.MergeCells.Cells {
		coordinates, err := f.areaRefToCoordinates(rng.Ref)
		if err != nil {
			return err
		}
		if coordinates[1] < row2 && row2 < coordinates[3] {
			return nil
		}
	}
	for i := 0; i < len(xlsx.MergeCells.Cells); i++ {
		areaData := xlsx.MergeCells.Cells[i]
		coordinates, _ := f.areaRefToCoordinates(areaData.Ref)
		x1, y1, x2, y2 := coordinates[0], coordinates[1], coordinates[2], coordinates[3]
		if y1 == y2 && y1 == row {
			from, _ := CoordinatesToCellName(x1, row2)
			to, _ := CoordinatesToCellName(x2, row2)
			if err := f.MergeCell(sheet, from, to); err != nil {
				return err
			}
			i++
		}
	}
	return nil
}

// checkRow provides a function to check and fill each column element for all
// rows and make that is continuous in a worksheet of XML. For example:
//
//    <row r="15" spans="1:22" x14ac:dyDescent="0.2">
//        <c r="A15" s="2" />
//        <c r="B15" s="2" />
//        <c r="F15" s="1" />
//        <c r="G15" s="1" />
//    </row>
//
// in this case, we should to change it to
//
//    <row r="15" spans="1:22" x14ac:dyDescent="0.2">
//        <c r="A15" s="2" />
//        <c r="B15" s="2" />
//        <c r="C15" s="2" />
//        <c r="D15" s="2" />
//        <c r="E15" s="2" />
//        <c r="F15" s="1" />
//        <c r="G15" s="1" />
//    </row>
//
// Noteice: this method could be very slow for large spreadsheets (more than
// 3000 rows one sheet).
func checkRow(xlsx *xlsxWorksheet) error {
	for rowIdx := range xlsx.SheetData.Row {
		rowData := &xlsx.SheetData.Row[rowIdx]

		colCount := len(rowData.C)
		if colCount == 0 {
			continue
		}
		lastCol, _, err := CellNameToCoordinates(rowData.C[colCount-1].R)
		if err != nil {
			return err
		}

		if colCount < lastCol {
			oldList := rowData.C
			newlist := make([]xlsxC, 0, lastCol)

			rowData.C = xlsx.SheetData.Row[rowIdx].C[:0]

			for colIdx := 0; colIdx < lastCol; colIdx++ {
				cellName, err := CoordinatesToCellName(colIdx+1, rowIdx+1)
				if err != nil {
					return err
				}
				newlist = append(newlist, xlsxC{R: cellName})
			}

			rowData.C = newlist

			for colIdx := range oldList {
				colData := &oldList[colIdx]
				colNum, _, err := CellNameToCoordinates(colData.R)
				if err != nil {
					return err
				}
				xlsx.SheetData.Row[rowIdx].C[colNum-1] = *colData
			}
		}
	}
	return nil
}

// convertRowHeightToPixels provides a function to convert the height of a
// cell from user's units to pixels. If the height hasn't been set by the user
// we use the default value. If the row is hidden it has a value of zero.
func convertRowHeightToPixels(height float64) float64 {
	var pixels float64
	if height == 0 {
		return pixels
	}
	pixels = math.Ceil(4.0 / 3.0 * height)
	return pixels
}
