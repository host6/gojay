package codegen

import (
	"fmt"
	"github.com/viant/toolbox"
	"strings"
)

type Struct struct {
	*toolbox.TypeInfo
	referenced *toolbox.TypeInfo
	*Generator
	Alias string
	Init  string
	Body  string
}

//Generate generates decoderCode + structRelease + encoderCode
func (s *Struct) Generate() (string, error) {
	return s.generateEncoding(s.TypeInfo)
}

func (s *Struct) generateEncoding(structInfo *toolbox.TypeInfo) (string, error) {
	var initEmbedded, decodingCases, err = s.generateFieldDecoding(structInfo.Fields())
	if err != nil {
		return "", err
	}

	encodingCases, err := s.generateFieldEncoding(structInfo.Fields())
	if err != nil {
		return "", err
	}
	var resetCode = ""
	if s.options.PoolObjects {
		resetCode, err = s.generateReset(structInfo.Fields())
		if err != nil {
			return "", err
		}
	}
	var data = struct {
		Receiver      string
		Alias         string
		InitEmbedded  string
		EncodingCases string
		DecodingCases string
		Reset         string
		FieldCount    int
	}{
		Receiver:      s.Alias + " *" + s.Name,
		DecodingCases: strings.Join(decodingCases, "\n"),
		EncodingCases: strings.Join(encodingCases, "\n"),
		FieldCount:    len(decodingCases),
		InitEmbedded:  initEmbedded,
		Reset:         resetCode,
		Alias:         s.Alias,
	}
	return expandBlockTemplate(encodingStructType, data)
}

func (s *Struct) generateReset(fields []*toolbox.FieldInfo) (string, error) {
	fieldReset, err := s.generateFieldReset(fields)
	if err != nil {
		return "", nil
	}
	return expandBlockTemplate(resetStruct, struct {
		Reset    string
		Receiver string
	}{
		Reset:    strings.Join(fieldReset, "\n"),
		Receiver: s.Alias + " *" + s.Name,
	})
}

func (s *Struct) generateFieldReset(fields []*toolbox.FieldInfo) ([]string, error) {
	fieldReset := []string{}
	for i := range fields {
		var templateKey = -1
		fieldTypeInfo := s.Type(normalizeTypeName(fields[i].TypeName))
		field, err := NewField(s, fields[i], fieldTypeInfo)
		if err != nil {
			return nil, err
		}
		if field.IsPointer || field.IsSlice || (fieldTypeInfo != nil && fieldTypeInfo.IsSlice) {
			templateKey = resetFieldValue
		} else {
			switch field.Type {
			case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "float32", "float64", "string", "bool", "[]string", "[]bool", "[]int", "[]int8", "[]int16", "[]int32", "[]int64", "[]uint", "[]uint8", "[]uint16", "[]uint32", "[]uint64", "[]float32", "[]float64", "[]byte":
				templateKey = resetFieldValue
			}
		}
		if templateKey != -1 {
			code, err := expandFieldTemplate(templateKey, field)
			if err != nil {
				return nil, err
			}
			fieldReset = append(fieldReset, code)
		}
	}
	return fieldReset, nil
}

func (s *Struct) generateFieldDecoding(fields []*toolbox.FieldInfo) (string, []string, error) {
	fieldCases := []string{}
	var initCode = ""
	for i := range fields {
		if isSkipable(s.options, fields[i]) {
			continue
		}
		var templateKey = -1
		fieldTypeInfo := s.Type(normalizeTypeName(fields[i].TypeName))
		field, err := NewField(s, fields[i], fieldTypeInfo)
		if err != nil {
			return "", nil, err
		}
		if fieldTypeInfo != nil {
			if err = s.generateStructCode(fieldTypeInfo.Name); err != nil {
				return "", nil, err
			}
		}

		if field.IsAnonymous {
			if fieldTypeInfo != nil {
				if field.IsPointer {
					init, err := expandBlockTemplate(embeddedStructInit, field)
					if err != nil {
						return "", nil, err
					}
					initCode += init
				}
				init, embeddedCases, err := s.generateFieldDecoding(fieldTypeInfo.Fields())
				if err != nil {
					return "", nil, err
				}
				initCode += init
				fieldCases = append(fieldCases, embeddedCases...)
			}
			continue
		}

	main:
		switch field.Type {
		case "string", "bool", "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "float32", "float64":
			templateKey = decodeBaseType
		case "[]string", "[]bool", "[]int", "[]int8", "[]int16", "[]int32", "[]int64", "[]uint", "[]uint8", "[]uint16", "[]uint32", "[]uint64", "[]float32", "[]float64":
			templateKey = decodeBaseTypeSlice
			s.generatePrimitiveArray(field)
		case "[]byte":
			templateKey = decodeRawType
		default:

			if fieldTypeInfo != nil {
				if !(field.IsSlice || fieldTypeInfo.IsSlice) {

					templateKey = decodeStruct
					break main
				}

				switch fieldTypeInfo.ComponentType {
				case "byte":
					templateKey = decodeRawType
					break main

				case "string", "bool", "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "float32", "float64":
					s.generatePrimitiveArray(field)
					templateKey = decodeBaseTypeSlice
					break main

				}

				if err = s.generateStructCode(field.ComponentType); err != nil {
					return "", nil, err
				}

				templateKey = decodeStructSlice
				if err = s.generateObjectArray(field); err != nil {
					return "", nil, err
				}

				break main
			} else if field.IsSlice {
				templateKey = decodeStructSlice
				if err = s.generateObjectArray(field); err != nil {
					return "", nil, err
				}
				// if is time.Time
			} else if strings.HasSuffix(field.Type, "time.Time") {
				templateKey = decodeTime
				s.addImport("time")
				// if is sql.NullXxx
			} else if strings.Contains(field.Type, "sql.Null") {
				templateKey = decodeSQLNull
				s.addImport("database/sql")
				if strings.Contains(field.Type, "Bool") {
					field.NullType = "Bool"
				} else if strings.Contains(field.Type, "Float64") {
					field.NullType = "Float64"
				} else if strings.Contains(field.Type, "Int64") {
					field.NullType = "Int64"
				} else if strings.Contains(field.Type, "String") {
					field.NullType = "String"
				} else {
					templateKey = decodeUnknown
				}
			} else {
				templateKey = decodeUnknown
			}
		}
		if templateKey != -1 {
			decodingCase, err := expandFieldTemplate(templateKey, field)
			if err != nil {
				return "", nil, err
			}
			fieldCases = append(fieldCases, decodingCase)
		}

	}
	return initCode, fieldCases, nil
}

func (s *Struct) generateEmbeddedFieldEncoding(field *Field, fieldTypeInfo *toolbox.TypeInfo) ([]string, error) {
	var result = []string{}
	if fieldTypeInfo != nil {
		embeddedCases, err := s.generateFieldEncoding(fieldTypeInfo.Fields())
		if err != nil {
			return nil, err
		}
		if field.IsPointer {
			result = append(result, fmt.Sprintf("    if %v != nil {", field.Accessor))
			for _, code := range embeddedCases {
				result = append(result, "    "+code)
			}
			result = append(result, "    }")
		} else {
			result = append(result, embeddedCases...)
		}
	}
	return result, nil
}

func (s *Struct) generateFieldEncoding(fields []*toolbox.FieldInfo) ([]string, error) {
	fieldCases := []string{}
	for i := range fields {
		if isSkipable(s.options, fields[i]) {
			continue
		}
		var templateKey = -1
		fieldTypeInfo := s.Type(normalizeTypeName(fields[i].TypeName))
		field, err := NewField(s, fields[i], fieldTypeInfo)
		if err != nil {
			return nil, err
		}
		if field.IsAnonymous {
			embedded, err := s.generateEmbeddedFieldEncoding(field, fieldTypeInfo)
			if err != nil {
				return nil, err
			}
			fieldCases = append(fieldCases, embedded...)
			continue
		}
	main:
		switch field.Type {
		case "string", "bool", "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "float32", "float64":
			templateKey = encodeBaseType
		case "[]string", "[]bool", "[]int", "[]int8", "[]int16", "[]int32", "[]int64", "[]uint", "[]uint8", "[]uint16", "[]uint32", "[]uint64", "[]float32", "[]float64":
			templateKey = encodeBaseTypeSlice
			s.generatePrimitiveArray(field)
		case "[]byte":
			templateKey = encodeRawType
		default:
			if fieldTypeInfo != nil {
				if !(field.IsSlice || fieldTypeInfo.IsSlice) {
					templateKey = encodeStruct
					break main
				}
				switch fieldTypeInfo.ComponentType {
				case "byte":
					templateKey = encodeRawType
					break main
				case "string", "bool", "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "float32", "float64":
					templateKey = decodeBaseTypeSlice
					break main
				}
				templateKey = encodeStructSlice
				break main
			} else if field.IsSlice {
				templateKey = encodeStructSlice
			} else if strings.HasSuffix(field.Type, "time.Time") {
				templateKey = encodeTime
			} else if strings.Contains(field.Type, "sql.Null") {
				templateKey = encodeSQLNull
				if strings.Contains(field.Type, "Bool") {
					field.NullType = "Bool"
				} else if strings.Contains(field.Type, "Float64") {
					field.NullType = "Float64"
				} else if strings.Contains(field.Type, "Int64") {
					field.NullType = "Int64"
				} else if strings.Contains(field.Type, "String") {
					field.NullType = "String"
				}
			} else {
				templateKey = encodeUnknown
			}
		}
		if templateKey != -1 {
			decodingCase, err := expandFieldTemplate(templateKey, field)
			if err != nil {
				return nil, err
			}
			fieldCases = append(fieldCases, decodingCase)
		}

	}
	return fieldCases, nil
}

func NewStruct(info *toolbox.TypeInfo, generator *Generator) *Struct {
	return &Struct{
		TypeInfo:  info,
		Generator: generator,
		Alias:     extractReceiverAlias(info.Name),
	}
}