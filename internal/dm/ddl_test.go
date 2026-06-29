package dm

import (
	"encoding/binary"
	"testing"
)

func TestFormatColumnTypeUsesScaleForNumericAndTimeTypes(t *testing.T) {
	tests := []struct {
		name     string
		dataType string
		length   uint32
		scale    int16
		want     string
	}{
		{name: "number with precision and scale", dataType: "NUMBER", length: 10, scale: 2, want: "NUMBER(10,2)"},
		{name: "number with precision only", dataType: "NUMBER", length: 11, scale: 0, want: "NUMBER(11)"},
		{name: "plain max precision number", dataType: "NUMBER", length: 38, scale: 0, want: "NUMBER"},
		{name: "datetime precision", dataType: "datetime", length: 8, scale: 6, want: "datetime(6)"},
		{name: "varchar length", dataType: "VARCHAR2", length: 50, scale: 0, want: "VARCHAR2(50)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatColumnType(tt.dataType, tt.length, tt.scale)
			if got != tt.want {
				t.Fatalf("formatColumnType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseDDLKeyInfoRecognizesDescendingOrder(t *testing.T) {
	keys := parseDDLKeyInfo([]byte{
		0x01, 0x00, 0x41,
		0x02, 0x00, 0x44,
		0x03, 0x00, 0x99,
	})

	if len(keys) != 3 {
		t.Fatalf("len(keys) = %d, want 3", len(keys))
	}
	if keys[0].ColID != 1 || keys[0].Order != "ASC" {
		t.Fatalf("keys[0] = %+v, want col 1 ASC", keys[0])
	}
	if keys[1].ColID != 2 || keys[1].Order != "DESC" {
		t.Fatalf("keys[1] = %+v, want col 2 DESC", keys[1])
	}
	if keys[2].ColID != 3 || keys[2].Order != "FLAG_0x99" {
		t.Fatalf("keys[2] = %+v, want col 3 FLAG_0x99", keys[2])
	}
}

func TestParseDDLRoleGrantRow(t *testing.T) {
	page := make([]byte, 256)
	rowOff := 0x40
	page[rowOff] = 0
	binary.LittleEndian.PutUint16(page[rowOff+1:], 44)
	binary.LittleEndian.PutUint32(page[rowOff+4:], 50331752)
	binary.LittleEndian.PutUint32(page[rowOff+8:], 67108866)
	binary.LittleEndian.PutUint32(page[rowOff+12:], ^uint32(0))
	binary.LittleEndian.PutUint32(page[rowOff+16:], ^uint32(0))
	binary.LittleEndian.PutUint32(page[rowOff+20:], ^uint32(0))
	page[rowOff+24] = 'N'

	grant, ok := parseDDLRoleGrantRow(page, rowOff, 208, 88, uint16(rowOff), 8192)
	if !ok {
		t.Fatal("parseDDLRoleGrantRow() returned false")
	}
	if grant.GranteeID != 50331752 || grant.RoleID != 67108866 || grant.AdminOption != "N" {
		t.Fatalf("grant = %+v", grant)
	}
}

func TestParseDDLObjectPrivilegeRow(t *testing.T) {
	page := make([]byte, 256)
	rowOff := 0x40
	page[rowOff] = 0
	binary.LittleEndian.PutUint16(page[rowOff+1:], 44)
	binary.LittleEndian.PutUint32(page[rowOff+4:], 50331752)
	binary.LittleEndian.PutUint32(page[rowOff+8:], 1063)
	binary.LittleEndian.PutUint32(page[rowOff+12:], ^uint32(0))
	binary.LittleEndian.PutUint32(page[rowOff+16:], 8192)
	binary.LittleEndian.PutUint32(page[rowOff+20:], 50331649)
	page[rowOff+24] = 'N'

	grant, ok := parseDDLObjectPrivilegeRow(page, rowOff, 208, 88, uint16(rowOff), 8192)
	if !ok {
		t.Fatal("parseDDLObjectPrivilegeRow() returned false")
	}
	if grant.GranteeID != 50331752 || grant.ObjectID != 1063 || grant.Privilege != "SELECT" || grant.Grantable != "N" {
		t.Fatalf("grant = %+v", grant)
	}
}

func TestParseDDLTextRow(t *testing.T) {
	page := make([]byte, 512)
	rowOff := 0x40
	text := []byte("CREATE OR REPLACE VIEW SYSDBA.V AS SELECT 1 AS ID")
	binary.LittleEndian.PutUint16(page[rowOff:], uint16(25+len(text)+8))
	binary.LittleEndian.PutUint32(page[rowOff+2:], 0x010004E2)
	binary.LittleEndian.PutUint32(page[rowOff+6:], 0)
	binary.LittleEndian.PutUint32(page[rowOff+21:], uint32(len(text)))
	copy(page[rowOff+25:], text)

	row, ok := parseDDLTextRow(page, rowOff, 1985, 12, uint16(rowOff), 8192, textDecoder{preferred: "utf-8"})
	if !ok {
		t.Fatal("parseDDLTextRow() returned false")
	}
	if row.ID != 0x010004E2 || row.SeqNo != 0 || row.Text != string(text) {
		t.Fatalf("row = %+v", row)
	}
}

func TestRenderCreateUserUsesPlaceholderPasswordAndTablespaces(t *testing.T) {
	user := dictionaryObject{Name: "HR_TEST", Info3: 4}
	tablespaces := map[uint32]string{3: "TEMP", 4: "MAIN"}
	got := renderCreateUser(user, tablespaces)
	want := `CREATE USER HR_TEST IDENTIFIED BY "dmdul_default_password" DEFAULT TABLESPACE "MAIN" TEMPORARY TABLESPACE "TEMP";`
	if got != want {
		t.Fatalf("renderCreateUser() = %q, want %q", got, want)
	}
}

func TestResolveSchemaNamePrefersDictionarySchemaRows(t *testing.T) {
	schemaNames := schemaNamesFromDictionaryObjects(map[uint32]dictionaryObject{
		150995944: {ID: 150995944, Name: "MMIS_INNOVATION", Type: "SCH", Valid: "Y"},
	})

	got := resolveSchemaName(150995944, schemaNames)
	if got != "MMIS_INNOVATION" {
		t.Fatalf("resolveSchemaName() = %q, want MMIS_INNOVATION", got)
	}
}

func TestQuoteIdentQuotesReservedWords(t *testing.T) {
	tests := map[string]string{
		"RIS":  "RIS",
		"KEY":  `"KEY"`,
		"TYPE": `"TYPE"`,
		"TIME": `"TIME"`,
		"中文字段": `"中文字段"`,
	}

	for input, want := range tests {
		if got := quoteIdent(input); got != want {
			t.Fatalf("quoteIdent(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDictionaryObjectTableFlags(t *testing.T) {
	heap := dictionaryObject{Info1: 0x10}
	if heap.isIOTTable() {
		t.Fatalf("heap.isIOTTable() = true, want false")
	}
	if got := heap.tableStorageOrganization(); got != heapStorageOrg {
		t.Fatalf("heap.tableStorageOrganization() = %q, want %q", got, heapStorageOrg)
	}

	iot := dictionaryObject{Info1: 0}
	if !iot.isIOTTable() {
		t.Fatalf("iot.isIOTTable() = false, want true")
	}
	if got := iot.tableStorageOrganization(); got != defaultStorageOrg {
		t.Fatalf("iot.tableStorageOrganization() = %q, want %q", got, defaultStorageOrg)
	}

	tempDelete := dictionaryObject{Info3: tableTemporaryInfo3Flag}
	if !tempDelete.isTemporaryTable() {
		t.Fatalf("tempDelete.isTemporaryTable() = false, want true")
	}
	if got := tempDelete.temporaryCommitClause(); got != tableTemporaryDeleteRowsClause {
		t.Fatalf("tempDelete.temporaryCommitClause() = %q, want %q", got, tableTemporaryDeleteRowsClause)
	}

	tempPreserve := dictionaryObject{Info3: tableTemporaryInfo3Flag | tableTemporarySessionInfo3Flag}
	if got := tempPreserve.temporaryCommitClause(); got != tableTemporaryPreserveRowsClause {
		t.Fatalf("tempPreserve.temporaryCommitClause() = %q, want %q", got, tableTemporaryPreserveRowsClause)
	}
}

func TestStorageClauseDistinguishesHeapAndIOTTables(t *testing.T) {
	tablespaces := map[uint32]string{4: "MAIN"}

	if got := storageClause(4, tablespaces, defaultStorageOrg); got != "\nSTORAGE(ON \"MAIN\", CLUSTERBTR)" {
		t.Fatalf("IOT storageClause() = %q", got)
	}
	if got := storageClause(4, tablespaces, heapStorageOrg); got != "\nSTORAGE(ON \"MAIN\", NOBRANCH)" {
		t.Fatalf("heap storageClause() = %q", got)
	}
}

func TestParseDDLPartitionRow(t *testing.T) {
	page := make([]byte, 8192)
	rowOff := 0x100
	page[rowOff] = 0x2C
	page[rowOff+1] = 0x40
	page[rowOff+2] = 0x00
	putUint32LE(page[rowOff+sysHPartBaseTableIDOffset:], 1044)
	putUint32LE(page[rowOff+sysHPartPartTableIDOffset:], 1045)
	pos := rowOff + sysHPartTypeOffset
	page[pos] = 0x85
	copy(page[pos+1:], []byte("RANGE"))
	pos += 6
	page[pos] = 0x87
	copy(page[pos+1:], []byte("P202401"))
	pos += 8
	copy(page[pos:], []byte("2024-02-01"))

	part, ok := parseDDLPartitionRow(page, rowOff, 10, 2, uint16(rowOff), 8192, textDecoder{preferred: "utf-8"})
	if !ok {
		t.Fatal("parseDDLPartitionRow() returned false")
	}
	if part.BaseTableID != 1044 || part.PartTableID != 1045 {
		t.Fatalf("partition ids = %d/%d, want 1044/1045", part.BaseTableID, part.PartTableID)
	}
	if part.Type != "RANGE" || part.Name != "P202401" {
		t.Fatalf("partition type/name = %q/%q, want RANGE/P202401", part.Type, part.Name)
	}
	if got := partitionHighValuePreview(part.HighValue); got != "2024-02-01" {
		t.Fatalf("partitionHighValuePreview() = %q, want 2024-02-01", got)
	}
}

func TestParseTabPartInfoAt(t *testing.T) {
	page := make([]byte, 256)
	pos := 64
	putUint32LE(page[pos-8:], 1049)
	page[pos] = 0x87
	copy(page[pos+1:], []byte("TABPART"))
	binOff := pos + 8
	page[binOff] = 0x87
	copy(page[binOff+1:], []byte{0x01, 0x00, 0x02, 0x00, 0x00, 0x00, 0x00})

	tableID, colIDs, ok := parseTabPartInfoAt(page, pos, 0, len(page))
	if !ok {
		t.Fatal("parseTabPartInfoAt() returned false")
	}
	if tableID != 1049 {
		t.Fatalf("tableID = %d, want 1049", tableID)
	}
	if len(colIDs) != 1 || colIDs[0] != 2 {
		t.Fatalf("colIDs = %+v, want [2]", colIDs)
	}
}

func putUint32LE(dst []byte, value uint32) {
	dst[0] = byte(value)
	dst[1] = byte(value >> 8)
	dst[2] = byte(value >> 16)
	dst[3] = byte(value >> 24)
}
