package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/alecthomas/kingpin/v2"
	"github.com/fatih/color"
	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
	"github.com/oschwald/maxminddb-golang/v2"
)

type Metadata struct {
	DatabaseType   string            `json:"database_type"`
	Description    map[string]string `json:"description"`
	Languages      []string          `json:"languages,omitempty"`
	BuildTimestamp *int64            `json:"build_epoch,omitempty"`
}

type InputData struct {
	Metadata Metadata     `json:"metadata"`
	Records  []JSONRecord `json:"records"`
}

type JSONRecord struct {
	Network string         `json:"network"`
	Data    map[string]any `json:"data"`
}

type ValidationError struct {
	Field   string
	Message string
}

// Add color variables
var (
	successColor = color.New(color.FgGreen).SprintFunc()
	errorColor   = color.New(color.FgRed).SprintFunc()
	warnColor    = color.New(color.FgYellow).SprintFunc()
	infoColor    = color.New(color.FgCyan).SprintFunc()
)

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error for %s: %s", e.Field, e.Message)
}

func validateMetadata(m Metadata) error {
	if m.DatabaseType == "" {
		return &ValidationError{
			Field:   "metadata.database_type",
			Message: "database_type is required",
		}
	}

	if m.Description == nil || len(m.Description) == 0 {
		return &ValidationError{
			Field:   "metadata.description",
			Message: "at least one description is required",
		}
	}

	// Validate description values
	for lang, desc := range m.Description {
		if lang == "" {
			return &ValidationError{
				Field:   "metadata.description",
				Message: "language code cannot be empty",
			}
		}
		if desc == "" {
			return &ValidationError{
				Field:   fmt.Sprintf("metadata.description.%s", lang),
				Message: "description cannot be empty",
			}
		}
	}

	// Validate languages if provided
	if len(m.Languages) > 0 {
		langMap := make(map[string]bool)
		for _, lang := range m.Languages {
			if lang == "" {
				return &ValidationError{
					Field:   "metadata.languages",
					Message: "language code cannot be empty",
				}
			}
			if langMap[lang] {
				return &ValidationError{
					Field:   "metadata.languages",
					Message: fmt.Sprintf("duplicate language code: %s", lang),
				}
			}
			langMap[lang] = true
		}

		// Verify all description languages are in languages list
		for lang := range m.Description {
			if !langMap[lang] {
				return &ValidationError{
					Field:   "metadata.languages",
					Message: fmt.Sprintf("description language '%s' not found in languages list", lang),
				}
			}
		}
	}

	if m.BuildTimestamp != nil {
		now := time.Now().Unix()
		if *m.BuildTimestamp > now {
			return &ValidationError{
				Field:   "metadata.build_epoch",
				Message: "build timestamp cannot be in the future",
			}
		}
	}

	return nil
}

func validateRecord(record JSONRecord) error {
	// Validate Network (CIDR)
	if record.Network == "" {
		return &ValidationError{
			Field:   "network",
			Message: "network is required",
		}
	}

	_, _, err := net.ParseCIDR(record.Network)
	if err != nil {
		return &ValidationError{
			Field:   "network",
			Message: fmt.Sprintf("invalid CIDR format: %v", err),
		}
	}

	// Validate Data
	if record.Data == nil {
		return &ValidationError{
			Field:   "data",
			Message: "data is required",
		}
	}

	if len(record.Data) == 0 {
		return &ValidationError{
			Field:   "data",
			Message: "data cannot be empty",
		}
	}

	// Validate data structure recursively
	if err := validateDataStructure(record.Data, "data"); err != nil {
		return err
	}

	return nil
}

func validateDataStructure(data interface{}, path string) error {
	if data == nil {
		return &ValidationError{
			Field:   path,
			Message: "value cannot be nil",
		}
	}

	switch v := data.(type) {
	case map[string]interface{}:
		if len(v) == 0 {
			return &ValidationError{
				Field:   path,
				Message: "map cannot be empty",
			}
		}
		for key, value := range v {
			if key == "" {
				return &ValidationError{
					Field:   path,
					Message: "map key cannot be empty",
				}
			}
			if err := validateDataStructure(value, fmt.Sprintf("%s.%s", path, key)); err != nil {
				return err
			}
		}
	case []interface{}:
		if len(v) == 0 {
			return &ValidationError{
				Field:   path,
				Message: "array cannot be empty",
			}
		}
		for i, value := range v {
			if err := validateDataStructure(value, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}

	return nil
}

// Add new type for collecting multiple validation errors
type ValidationErrors struct {
	Errors []ValidationError
}

func (ve *ValidationErrors) Add(field, message string) {
	ve.Errors = append(ve.Errors, ValidationError{
		Field:   field,
		Message: message,
	})
}

func (ve *ValidationErrors) HasErrors() bool {
	return len(ve.Errors) > 0
}

// Modify the main function to add check mode
func main() {
	app := kingpin.New("mmdbimport", "A tool to import JSON into MMDB files")
	app.HelpFlag.Short('h')
	app.UsageWriter(os.Stdout)

	// Add check mode flag
	checkFile := app.Flag("check", "Check JSON file for errors without building MMDB").
		Short('c').
		ExistingFile()

	inputFile := app.Flag("input", "Input JSON file path").
		Short('i').
		ExistingFile()

	verifyFile := app.Flag("verify", "Verify and display MMDB file information").
		Short('v').
		ExistingFile()

	verifyVerbose := app.Flag("verify-verbose", "Verify and display MMDB file information").
		Short('V').
		ExistingFile()

	jsonOutput := app.Flag("json", "Output in JSON format").
		Bool()

	outputFile := app.Flag("output", "Output MMDB file path").
		Short('o').
		Default("output.mmdb").
		String()

	recordSize := app.Flag("record-size", "Record size (24, 28, or 32)").
		Short('r').
		Default("28").
		Enum("24", "28", "32")

	// Show usage if no args or --help
	if len(os.Args) == 1 {
		app.Usage(os.Args[1:])
		os.Exit(0)
	}

	kingpin.MustParse(app.Parse(os.Args[1:]))

	// Count how many mode flags are set
	modeFlags := 0
	if *checkFile != "" {
		// log.Printf("checkFile: %s", *checkFile)
		modeFlags++
	}
	if *inputFile != "" {
		// log.Printf("inputFile: %s", *inputFile)
		modeFlags++
	}
	if *verifyFile != "" {
		// log.Printf("verifyFile: %s", *verifyFile)
		modeFlags++
	}
	if *verifyVerbose != "" {
		// log.Printf("verifyVerbose: %s", *verifyVerbose)
		// *inputFile = *verifyFile
		modeFlags++
	}
	// log.Printf("modeFlags: %d", modeFlags)

	// Validate mode flags
	if modeFlags == 0 {
		log.Fatal(errorColor("One of --check, --input, --verify, --verify-verbose flags must be provided"))
	}
	if modeFlags > 1 {
		log.Fatal(errorColor("The --check, --input, --verify, --verify-verbose flags are mutually exclusive"))
	}

	// Handle verify mode
	if *verifyFile != "" {
		if err := verifyMMDBFile(*verifyFile, false, *jsonOutput); err != nil {
			log.Fatal(errorColor(fmt.Sprintf("Error verifying MMDB file: %v", err)))
		}
		os.Exit(0)
	}
	// Handle verify verbose mode
	if *verifyVerbose != "" {
		if err := verifyMMDBFile(*verifyVerbose, true, *jsonOutput); err != nil {
			log.Fatal(errorColor(fmt.Sprintf("Error verifying MMDB file: %v", err)))
		}
		os.Exit(0)
	}

	// Handle check mode
	if *checkFile != "" {
		if err := validateJSONFile(*checkFile); err != nil {
			os.Exit(1)
		}
		fmt.Printf("%s %s\n", successColor("✓"), infoColor("JSON validation successful"))
		os.Exit(0)
	}

	// Regular build mode requires input file
	if *inputFile == "" {
		log.Fatal(errorColor("Input file is required for build mode. Use -i or --input"))
	}

	// Validate input file before processing
	if err := validateJSONFile(*inputFile); err != nil {
		log.Fatal(errorColor("Invalid input file"))
	}

	// Convert recordSize from string to int
	recordSizeInt := 28
	switch *recordSize {
	case "24":
		recordSizeInt = 24
	case "28":
		recordSizeInt = 28
	case "32":
		recordSizeInt = 32
	}

	// Read and parse JSON file
	inputData, err := readJSONFile(*inputFile)
	if err != nil {
		log.Fatal(errorColor(fmt.Sprintf("Error reading JSON file: %v", err)))
	}

	// Validate metadata
	if err := validateMetadata(inputData.Metadata); err != nil {
		log.Fatal(errorColor(fmt.Sprintf("Invalid metadata: %v", err)))
	}

	// Validate records
	for i, record := range inputData.Records {
		if err := validateRecord(record); err != nil {
			log.Fatal(errorColor(fmt.Sprintf("Invalid record at index %d: %v", i, err)))
		}
	}

	// Detect IP version from records
	ipVersion := detectIPVersion(inputData.Records)
	log.Printf("%s: %d", infoColor("Detected IP version"), ipVersion)

	// Set default metadata values
	if inputData.Metadata.Languages == nil || len(inputData.Metadata.Languages) == 0 {
		inputData.Metadata.Languages = []string{"en"}
	}
	if inputData.Metadata.BuildTimestamp == nil {
		now := time.Now().Unix()
		inputData.Metadata.BuildTimestamp = &now
	}

	// Create MMDB writer with metadata (note: BinaryVersion is always 2)
	writer, err := mmdbwriter.New(mmdbwriter.Options{
		DatabaseType: inputData.Metadata.DatabaseType,
		Description:  inputData.Metadata.Description,
		Languages:    inputData.Metadata.Languages,
		IPVersion:    ipVersion,
		RecordSize:   recordSizeInt,
		// BinaryFormatMajorVersion is not set as it defaults to 2 in mmdbwriter
	})
	if err != nil {
		log.Fatalf("Error creating MMDB writer: %v", err)
	}

	// Process records
	for i, record := range inputData.Records {
		if err := processRecord(writer, record, i); err != nil {
			log.Printf("Warning: Error processing record %d: %v", i, err)
		}
	}

	// Write the database to file
	if err := writeDatabase(writer, *outputFile); err != nil {
		log.Fatalf("Error writing database: %v", err)
	}

	log.Printf("%s: %s", successColor("Successfully created MMDB file"), *outputFile)
}

func detectIPVersion(records []JSONRecord) int {
	hasIPv4 := false
	hasIPv6 := false

	for _, record := range records {
		ip, _, err := net.ParseCIDR(record.Network)
		if err != nil {
			continue
		}

		if ip.To4() != nil {
			hasIPv4 = true
		} else {
			hasIPv6 = true
		}

		// If we found both types, we can stop searching
		if hasIPv4 && hasIPv6 {
			break
		}
	}

	// Decision logic
	switch {
	case hasIPv4 && hasIPv6:
		return 6 // MMDB supports both when set to 6
	case hasIPv6:
		return 6
	case hasIPv4:
		return 4
	default:
		return 6 // Default to IPv6 if no valid IPs found
	}
}

func readJSONFile(filepath string) (InputData, error) {
	data, err := os.ReadFile(filepath)
	if err != nil {
		return InputData{}, fmt.Errorf("reading file: %w", err)
	}

	var input InputData
	if err := json.Unmarshal(data, &input); err != nil {
		// Try legacy format (just array of records)
		var records []JSONRecord
		if err := json.Unmarshal(data, &records); err != nil {
			return InputData{}, fmt.Errorf("parsing JSON: %w", err)
		}
		input.Records = records
	}

	return input, nil
}

func processRecord(writer *mmdbwriter.Tree, record JSONRecord, index int) error {
	_, network, err := net.ParseCIDR(record.Network)
	if err != nil {
		return fmt.Errorf(errorColor("parsing network %s: %v"), record.Network, err)
	}

	data, err := convertToMMDBType(record.Data)
	if err != nil {
		return fmt.Errorf(errorColor("converting data: %v"), err)
	}

	if err := writer.Insert(network, data); err != nil {
		return fmt.Errorf(errorColor("inserting record: %v"), err)
	}

	return nil
}

func writeDatabase(writer *mmdbwriter.Tree, filepath string) error {
	f, err := os.Create(filepath)
	if err != nil {
		return fmt.Errorf("creating output file: %w", err)
	}
	defer f.Close()

	_, err = writer.WriteTo(f)
	return err
}

func convertToMMDBType(data interface{}) (mmdbtype.DataType, error) {
	if data == nil {
		return mmdbtype.String(""), nil
	}

	switch v := data.(type) {
	case string:
		return mmdbtype.String(v), nil
	case int:
		return mmdbtype.Int32(v), nil
	case int32:
		return mmdbtype.Int32(v), nil
	case float32:
		return mmdbtype.Float32(v), nil
	case float64:
		return mmdbtype.Float64(v), nil
	case uint:
		return mmdbtype.Uint32(v), nil
	case uint32:
		return mmdbtype.Uint32(v), nil
	case uint64:
		return mmdbtype.Uint64(v), nil
	case bool:
		return mmdbtype.Bool(v), nil
	case []interface{}: // Handle arrays of any type
		return convertSlice(v)
	case map[string]interface{}: // Handle nested objects
		return convertMap(v)
	case []byte: // Handle binary data
		return mmdbtype.Bytes(v), nil
	default:
		// Handle custom types using reflection
		return handleCustomType(v)
	}
}

func convertSlice(slice []interface{}) (mmdbtype.Slice, error) {
	result := make(mmdbtype.Slice, len(slice))
	for i, item := range slice {
		converted, err := convertToMMDBType(item)
		if err != nil {
			return nil, fmt.Errorf("converting slice item %d: %w", i, err)
		}
		result[i] = converted
	}
	return result, nil
}

func convertMap(m map[string]interface{}) (mmdbtype.Map, error) {
	result := make(mmdbtype.Map)
	for key, value := range m {
		converted, err := convertToMMDBType(value)
		if err != nil {
			return nil, fmt.Errorf("converting map key %s: %w", key, err)
		}
		result[mmdbtype.String(key)] = converted
	}
	return result, nil
}

func handleCustomType(v interface{}) (mmdbtype.DataType, error) {
	val := reflect.ValueOf(v)

	switch val.Kind() {
	case reflect.Slice, reflect.Array:
		return convertReflectSlice(val)
	case reflect.Map:
		return convertReflectMap(val)
	case reflect.Struct:
		return convertReflectStruct(val)
	case reflect.Ptr:
		if val.IsNil() {
			return mmdbtype.String(""), nil
		}
		return convertToMMDBType(val.Elem().Interface())
	default:
		// Try to convert to string as fallback
		return mmdbtype.String(fmt.Sprintf("%v", v)), nil
	}
}

func convertReflectSlice(val reflect.Value) (mmdbtype.Slice, error) {
	length := val.Len()
	result := make(mmdbtype.Slice, length)

	for i := 0; i < length; i++ {
		item := val.Index(i).Interface()
		converted, err := convertToMMDBType(item)
		if err != nil {
			return nil, fmt.Errorf("converting reflect slice item %d: %w", i, err)
		}
		result[i] = converted
	}

	return result, nil
}

func convertReflectMap(val reflect.Value) (mmdbtype.Map, error) {
	result := make(mmdbtype.Map)

	for _, key := range val.MapKeys() {
		// Convert key to string
		keyStr := fmt.Sprintf("%v", key.Interface())

		// Convert value
		value := val.MapIndex(key).Interface()
		converted, err := convertToMMDBType(value)
		if err != nil {
			return nil, fmt.Errorf("converting reflect map key %s: %w", keyStr, err)
		}

		result[mmdbtype.String(keyStr)] = converted
	}

	return result, nil
}

func convertReflectStruct(val reflect.Value) (mmdbtype.Map, error) {
	result := make(mmdbtype.Map)
	typ := val.Type()

	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		fieldType := typ.Field(i)

		// Skip unexported fields
		if !fieldType.IsExported() {
			continue
		}

		// Get field name from json tag or use struct field name
		fieldName := fieldType.Tag.Get("json")
		if fieldName == "" {
			fieldName = fieldType.Name
		}

		// Convert value
		converted, err := convertToMMDBType(field.Interface())
		if err != nil {
			return nil, fmt.Errorf("converting struct field %s: %w", fieldName, err)
		}

		result[mmdbtype.String(fieldName)] = converted
	}

	return result, nil
}

// New function to validate JSON file and collect all errors
func validateJSONFile(filepath string) error {
	inputData, err := readJSONFile(filepath)
	if err != nil {
		log.Printf("%s: Error reading JSON file: %v", errorColor("Error"), err)
		return err
	}

	// Print file info
	fmt.Printf("%s %s\n", infoColor("Input file:"), filepath)

	ve := &ValidationErrors{}

	// Validate metadata
	if err := validateMetadataCollectErrors(inputData.Metadata, ve); err != nil {
		return err
	}

	// Print metadata info if no validation errors
	if !ve.HasErrors() {
		// Detect IP version
		ipVersion := detectIPVersion(inputData.Records)
		ipVersionStr := fmt.Sprintf("%d", ipVersion)
		if ipVersion == 6 {
			ipVersionStr += " (supports both IPv4 and IPv6)"
		}

		fmt.Printf("\n%s\n", infoColor("Database Information:"))
		fmt.Printf("  IP Version: %s\n", successColor(ipVersionStr))
		fmt.Printf("  Total Records: %s\n", successColor(fmt.Sprintf("%d", len(inputData.Records))))

		fmt.Printf("\n%s\n", infoColor("Metadata:"))
		fmt.Printf("  Database Type: %s\n", successColor(inputData.Metadata.DatabaseType))

		fmt.Printf("  Description:\n")
		for lang, desc := range inputData.Metadata.Description {
			fmt.Printf("    %s: %s\n", successColor(lang), desc)
		}

		if len(inputData.Metadata.Languages) > 0 {
			fmt.Printf("  Languages: %s\n", successColor(joinStrings(inputData.Metadata.Languages)))
		}

		if inputData.Metadata.BuildTimestamp != nil {
			timestamp := time.Unix(*inputData.Metadata.BuildTimestamp, 0)
			fmt.Printf("  Build Timestamp: %s\n", successColor(timestamp.Format(time.RFC3339)))
		}
	}

	// Validate all records
	for i, record := range inputData.Records {
		if err := validateRecordCollectErrors(record, i, ve); err != nil {
			return err
		}
	}

	if ve.HasErrors() {
		// Print all collected errors
		fmt.Printf("\n%s: Found %d validation errors:\n", errorColor("Validation failed"), len(ve.Errors))
		for _, err := range ve.Errors {
			fmt.Printf("  %s: %s\n", warnColor(err.Field), err.Message)
		}
		return fmt.Errorf("validation failed")
	}

	return nil
}

// Helper function to join strings with commas
func joinStrings(strs []string) string {
	return strings.Join(strs, ", ")
}

// Modified validation functions to collect all errors
func validateMetadataCollectErrors(m Metadata, ve *ValidationErrors) error {
	if m.DatabaseType == "" {
		ve.Add("metadata.database_type", "database_type is required")
	}

	if m.Description == nil || len(m.Description) == 0 {
		ve.Add("metadata.description", "at least one description is required")
	}

	// Validate description values
	for lang, desc := range m.Description {
		if lang == "" {
			ve.Add("metadata.description", "language code cannot be empty")
		}
		if desc == "" {
			ve.Add(fmt.Sprintf("metadata.description.%s", lang), "description cannot be empty")
		}
	}

	// Validate languages if provided
	if len(m.Languages) > 0 {
		langMap := make(map[string]bool)
		for _, lang := range m.Languages {
			if lang == "" {
				ve.Add("metadata.languages", "language code cannot be empty")
			}
			if langMap[lang] {
				ve.Add("metadata.languages", fmt.Sprintf("duplicate language code: %s", lang))
			}
			langMap[lang] = true
		}

		// Verify all description languages are in languages list
		for lang := range m.Description {
			if !langMap[lang] {
				ve.Add("metadata.languages", fmt.Sprintf("description language '%s' not found in languages list", lang))
			}
		}
	}

	if m.BuildTimestamp != nil {
		now := time.Now().Unix()
		if *m.BuildTimestamp > now {
			ve.Add("metadata.build_epoch", "build timestamp cannot be in the future")
		}
	}

	return nil
}

func validateRecordCollectErrors(record JSONRecord, index int, ve *ValidationErrors) error {
	fieldPrefix := fmt.Sprintf("records[%d]", index)

	if record.Network == "" {
		ve.Add(fieldPrefix+".network", "network is required")
		return nil
	}

	if _, _, err := net.ParseCIDR(record.Network); err != nil {
		ve.Add(fieldPrefix+".network", fmt.Sprintf("invalid CIDR format: %v", err))
	}

	if record.Data == nil {
		ve.Add(fieldPrefix+".data", "data is required")
		return nil
	}

	if len(record.Data) == 0 {
		ve.Add(fieldPrefix+".data", "data cannot be empty")
	}

	// Validate data structure recursively
	validateDataStructureCollectErrors(record.Data, fieldPrefix+".data", ve)

	return nil
}

func validateDataStructureCollectErrors(data interface{}, path string, ve *ValidationErrors) {
	if data == nil {
		ve.Add(path, "value cannot be nil")
		return
	}

	switch v := data.(type) {
	case map[string]interface{}:
		if len(v) == 0 {
			ve.Add(path, "map cannot be empty")
			return
		}
		for key, value := range v {
			if key == "" {
				ve.Add(path, "map key cannot be empty")
			}
			validateDataStructureCollectErrors(value, fmt.Sprintf("%s.%s", path, key), ve)
		}
	case []interface{}:
		if len(v) == 0 {
			ve.Add(path, "array cannot be empty")
			return
		}
		for i, value := range v {
			validateDataStructureCollectErrors(value, fmt.Sprintf("%s[%d]", path, i), ve)
		}
	}
}

// Add struct for JSON output
type VerifyOutput struct {
	Filepath      string            `json:"filepath"`
	BinaryFormat  string            `json:"binary_format"`
	IPVersion     int               `json:"ip_version"`
	RecordSize    int               `json:"record_size"`
	NodeCount     uint              `json:"node_count"`
	DatabaseType  string            `json:"database_type"`
	Description   map[string]string `json:"description"`
	Languages     []string          `json:"languages"`
	BuildTime     string            `json:"build_time"`
	BuildTimeAge  int               `json:"build_time_age"`
	TotalNetworks int               `json:"total_networks"`
	Networks      []NetworkEntry    `json:"networks,omitempty"`
}

type NetworkEntry struct {
	Position int         `json:"position"`
	Network  string      `json:"network"`
	Data     interface{} `json:"data"`
}

func verifyMMDBFile(filepath string, verbose bool, jsonOutput bool) error {
	reader, err := maxminddb.Open(filepath)
	if err != nil {
		return fmt.Errorf("opening MMDB file: %w", err)
	}
	defer reader.Close()

	// Get metadata
	metadata := reader.Metadata
	buildTime := time.Unix(int64(metadata.BuildEpoch), 0)
	networks := countNetworks(reader)
	if jsonOutput {
		output := VerifyOutput{
			Filepath:     filepath,
			BinaryFormat: fmt.Sprintf("%d.%d", metadata.BinaryFormatMajorVersion, metadata.BinaryFormatMinorVersion),
			IPVersion:    int(metadata.IPVersion),
			RecordSize:   int(metadata.RecordSize),
			NodeCount:    metadata.NodeCount,
			DatabaseType: metadata.DatabaseType,
			Description:  metadata.Description,
			Languages:    metadata.Languages,
			BuildTime:    buildTime.Format(time.RFC3339),
			// Build time age in seconds
			BuildTimeAge:  int(time.Since(buildTime).Seconds()),
			TotalNetworks: networks,
		}
		if verbose {
			output.Networks = []NetworkEntry{}
			for result := range reader.Networks() {
				var record interface{}
				if err := result.Decode(&record); err != nil {
					continue
				}
				output.Networks = append(output.Networks, NetworkEntry{
					Network: result.Prefix().String(),
					Data:    record,
				})
			}
		}
		// output nicely formatted json
		jsonOutput, err := json.MarshalIndent(output, "", "  ")
		if err != nil {
			return fmt.Errorf("marshalling JSON: %w", err)
		}

		fmt.Printf("%s\n", string(jsonOutput))
	} else {
		// Print file info
		fmt.Printf("%s %s\n", infoColor("MMDB file:"), filepath)

		fmt.Printf("  Build Timestamp: %s\n", successColor(buildTime.Format(time.RFC3339)))

		fmt.Printf("\n%s\n", infoColor("Database Information:"))
		fmt.Printf("  Binary Format: %s\n", successColor(fmt.Sprintf("%d.%d",
			metadata.BinaryFormatMajorVersion,
			metadata.BinaryFormatMinorVersion)))
		fmt.Printf("  IP Version: %s\n", successColor(fmt.Sprintf("%d", metadata.IPVersion)))
		fmt.Printf("  Record Size: %s bits\n", successColor(fmt.Sprintf("%d", metadata.RecordSize)))
		fmt.Printf("  Node Count: %s\n", successColor(fmt.Sprintf("%d", metadata.NodeCount)))
		// fmt.Printf("  Search Tree Size: %s bytes\n", successColor(fmt.Sprintf("%d", metadata.)))

		fmt.Printf("\n%s\n", infoColor("Metadata:"))
		fmt.Printf("  Database Type: %s\n", successColor(metadata.DatabaseType))

		fmt.Printf("  Description:\n")
		for lang, desc := range metadata.Description {
			fmt.Printf("    %s: %s\n", successColor(lang), desc)
		}

		if len(metadata.Languages) > 0 {
			fmt.Printf("  Languages: %s\n", successColor(joinStrings(metadata.Languages)))
		}
		fmt.Printf("\n%s\n", infoColor("Statistics:"))
		fmt.Printf("  Total Networks: %s\n", successColor(fmt.Sprintf("%d", networks)))
		// Add networks listing in verbose mode
		if verbose {
			fmt.Printf("\n%s\n", infoColor("Networks:"))
			position := 0
			for result := range reader.Networks() {
				var record interface{}
				err := result.Decode(&record)
				if err != nil {
					continue
				}
				fmt.Printf("[%d]  %s: %v\n", position, successColor(result.Prefix()), record)
				position++
			}
		}
	}

	return nil
}

// Update countNetworks function to use maxminddb
func countNetworks(db *maxminddb.Reader) int {
	count := 0
	// networks := db.Networks(maxminddb.IncludeNetworksWithoutData)
	for result := range db.Networks() {
		record := struct {
			Domain string `maxminddb:"connection_type"`
		}{}
		// we should iterate over the networks, to validate the data
		err := result.Decode(&record)
		if err != nil {
			log.Panic(err)
		}
		count++
		// fmt.Printf("%s: %s\n", result.Prefix(), record.Domain)
	}

	return count
}
