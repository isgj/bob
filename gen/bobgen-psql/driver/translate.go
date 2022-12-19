package driver

import (
	"fmt"
	"os"
	"strings"

	"github.com/stephenafamo/bob/gen/drivers"
	"github.com/stephenafamo/bob/gen/importers"
	"github.com/volatiletech/strmangle"
)

// translateColumnType converts postgres database types to Go types, for example
// "varchar" to "string" and "bigint" to "int64". It returns this parsed data
// as a Column object.
func (p *Driver) translateColumnType(c drivers.Column) drivers.Column {
	switch c.DBType {
	case "bigint", "bigserial":
		c.Type = "int64"
	case "integer", "serial":
		c.Type = "int"
	case "oid":
		c.Type = "uint32"
	case "smallint", "smallserial":
		c.Type = "int16"
	case "decimal", "numeric":
		c.Type = "types.Decimal"
	case "double precision":
		c.Type = "float64"
	case "real":
		c.Type = "float32"
	case "bit", "interval", "uuint", "bit varying", "character", "money", "character varying", "cidr", "inet", "macaddr", "text", "uuid", "xml":
		c.Type = "string"
	case `"char"`:
		c.Type = "types.Byte"
	case "json", "jsonb":
		c.Type = "types.JSON"
	case "bytea":
		c.Type = "[]byte"
	case "boolean":
		c.Type = "bool"
	case "date", "time", "timestamp without time zone", "timestamp with time zone", "time without time zone", "time with time zone":
		c.Type = "time.Time"
	case "point":
		c.Type = "pgeo.Point"
	case "line":
		c.Type = "pgeo.Line"
	case "lseg":
		c.Type = "pgeo.Lseg"
	case "box":
		c.Type = "pgeo.Box"
	case "path":
		c.Type = "pgeo.Path"
	case "polygon":
		c.Type = "pgeo.Polygon"
	case "circle":
		c.Type = "pgeo.Circle"
	case "ARRAY":
		var dbType string
		if _, ok := p.enums[c.UDTName[1:]]; ok {
			enumName := c.UDTName[1:]
			dbType = fmt.Sprintf("enum.%s", enumName)
			c.Type = fmt.Sprintf("EnumArray[%s]", strmangle.TitleCase(enumName))
		} else {
			c.Type, dbType = getArrayType(c)
		}
		// Make DBType something like ARRAYinteger for parsing with randomize.Struct
		c.DBType += dbType
	case "USER-DEFINED":
		switch c.UDTName {
		case "hstore":
			c.Type = "types.HStore"
			c.DBType = "hstore"
		case "citext":
			c.Type = "string"
		default:
			c.Type = "string"
			fmt.Fprintf(os.Stderr, "warning: incompatible data type detected: %s\n", c.UDTName)
		}
	default:
		if strings.HasPrefix(c.DBType, "enum.") {
			c.Type = strmangle.TitleCase(strings.TrimPrefix(c.DBType, "enum."))
		} else {
			c.Type = "string"
		}
	}

	c.Imports = typMap[c.Type]
	return c
}

// getArrayType returns the correct Array type for each database type
func getArrayType(c drivers.Column) (string, string) {
	// If a domain is created with a statement like this: "CREATE DOMAIN
	// text_array AS TEXT[] CHECK ( ... )" then the array type will be null,
	// but the udt name will be whatever the underlying type is with a leading
	// underscore. Note that this code handles some types, but not nearly all
	// the possibities. Notably, an array of a user-defined type ("CREATE
	// DOMAIN my_array AS my_type[]") will be treated as an array of strings,
	// which is not guaranteed to be correct.
	if c.ArrType != nil {
		switch *c.ArrType {
		case "bigint", "bigserial", "integer", "serial", "smallint", "smallserial", "oid":
			return "types.Int64Array", *c.ArrType
		case "bytea":
			return "types.BytesArray", *c.ArrType
		case "bit", "interval", "uuint", "bit varying", "character", "money", "character varying", "cidr", "inet", "macaddr", "text", "uuid", "xml":
			return "types.StringArray", *c.ArrType
		case "boolean":
			return "types.BoolArray", *c.ArrType
		case "decimal", "numeric":
			return "types.DecimalArray", *c.ArrType
		case "double precision", "real":
			return "types.Float64Array", *c.ArrType
		default:
			return "types.StringArray", *c.ArrType
		}
	} else {
		switch c.UDTName {
		case "_int4", "_int8":
			return "types.Int64Array", c.UDTName
		case "_bytea":
			return "types.BytesArray", c.UDTName
		case "_bit", "_interval", "_varbit", "_char", "_money", "_varchar", "_cidr", "_inet", "_macaddr", "_citext", "_text", "_uuid", "_xml":
			return "types.StringArray", c.UDTName
		case "_bool":
			return "types.BoolArray", c.UDTName
		case "_numeric":
			return "types.DecimalArray", c.UDTName
		case "_float4", "_float8":
			return "types.Float64Array", c.UDTName
		default:
			return "types.StringArray", c.UDTName
		}
	}
}

//nolint:gochecknoglobals
var typMap = map[string]importers.List{
	"time.Time":          {`"time"`},
	"types.JSON":         {`"github.com/volatiletech/sqlboiler/v4/types"`},
	"types.Decimal":      {`"github.com/volatiletech/sqlboiler/v4/types"`},
	"types.BytesArray":   {`"github.com/volatiletech/sqlboiler/v4/types"`},
	"types.Int64Array":   {`"github.com/volatiletech/sqlboiler/v4/types"`},
	"types.Float64Array": {`"github.com/volatiletech/sqlboiler/v4/types"`},
	"types.BoolArray":    {`"github.com/volatiletech/sqlboiler/v4/types"`},
	"types.StringArray":  {`"github.com/volatiletech/sqlboiler/v4/types"`},
	"types.DecimalArray": {`"github.com/volatiletech/sqlboiler/v4/types"`},
	"types.HStore":       {`"github.com/volatiletech/sqlboiler/v4/types"`},
	"pgeo.Point":         {`"github.com/volatiletech/sqlboiler/v4/types/pgeo"`},
	"pgeo.Line":          {`"github.com/volatiletech/sqlboiler/v4/types/pgeo"`},
	"pgeo.Lseg":          {`"github.com/volatiletech/sqlboiler/v4/types/pgeo"`},
	"pgeo.Box":           {`"github.com/volatiletech/sqlboiler/v4/types/pgeo"`},
	"pgeo.Path":          {`"github.com/volatiletech/sqlboiler/v4/types/pgeo"`},
	"pgeo.Polygon":       {`"github.com/volatiletech/sqlboiler/v4/types/pgeo"`},
}