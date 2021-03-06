package main

import (
	"database/sql"
	"github.com/gocodo/bloomdb"
	"github.com/bloomapi/dataloading"
	"gopkg.in/yaml.v2"
	"github.com/spf13/viper"
	"io/ioutil"
	"encoding/csv"
	"os"
	"strings"
	"strconv"

	"fmt"
)

type tableColumnInfo struct {
	Name string
	Type string
}

func tableColumns(conn *sql.DB, table string) ([]tableColumnInfo, error) {
	columns := []tableColumnInfo{}
	rows, err := conn.Query(`	SELECT column_name, data_type 
														FROM information_schema.columns
													 	WHERE table_name = '` + table + `';`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var name, columnType string
		if err := rows.Scan(&name, &columnType); err != nil {
			return nil, err
		}

		columns = append(columns, tableColumnInfo{
				name,
				columnType,
			})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return columns, nil
}

func main() {

	viper.SetConfigName("config")
	viper.SetConfigType("toml")
	viper.AddConfigPath("./")

	configPath := os.Getenv("BLOOM_CONFIG")
	if configPath != "" {
		viper.AddConfigPath(configPath)
	}

	err := viper.ReadInConfig()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}


	tableNames := []string{}

	bloomdb := bloomdb.CreateDB()

	file, err := ioutil.ReadFile("../../dbmapping.yaml")
	if err != nil {
		fmt.Println(err)
		return
	}

	mapping := dataloading.SourceMapping{}
	err = yaml.Unmarshal(file, &mapping)
	if err != nil {
		fmt.Println(err)
		return
	}

	for _, source := range mapping.Sources {
		for _, dest := range source.Destinations {
			tableNames = append(tableNames, dest.Name)
		}
	}

	conn, err := bloomdb.SqlConnection()
	if err != nil {
		fmt.Println(err)
		return
	}

	beneColumns := []string{}
	beneInfos, err := tableColumns(conn, "usgov_hhs_cclf_beneficiaries")
	if err != nil {
		fmt.Println(err)
		return
	}

	for _, info := range beneInfos {
		if info.Type != "uuid" && !strings.HasPrefix(info.Name, "bloom_") {
			beneColumns = append(beneColumns, info.Name)
		}
	}

	for _, table := range tableNames {
		fmt.Println("Exporting " + table)
		foundHicn := false
		csvfile, err := os.Create(table + ".csv")
		if err != nil {
			fmt.Println(err)
			return
		}
		defer csvfile.Close()
		writer := csv.NewWriter(csvfile)

		columns := []string{}
		infos, err := tableColumns(conn, table)
		if err != nil {
			fmt.Println(err)
			return
		}

		for _, info := range infos {
			if info.Name == "hic_id" && table != "usgov_hhs_cclf_beneficiaries" {
				foundHicn = true
			}
			if info.Type != "uuid" && !strings.HasPrefix(info.Name, "bloom_") {
				columns = append(columns, table + "." + info.Name)
			}
		}

		var query string
		if foundHicn {
			for _, name := range beneColumns {
				columns = append(columns, "usgov_hhs_cclf_beneficiaries." + name)
			}
			query = "SELECT " + strings.Join(columns, ",") + " FROM " + table + `
join usgov_hhs_cclf_beneficiaries
on usgov_hhs_cclf_beneficiaries.id =  (
	select id from usgov_hhs_cclf_beneficiaries
	where usgov_hhs_cclf_beneficiaries.hic_id = ` + table + `.hic_id
	order by date_beneficiary_enrolled_in_hospice
	limit 1
)
			`
		} else {
			query = "SELECT " + strings.Join(columns, ",") + " FROM " + table
		}

		writer.Write(columns)

		rows, err := conn.Query(query)
		if err != nil {
			fmt.Println(err)
			return
		}
		defer rows.Close()

		for rows.Next() {
			resultRefs := make([]interface{}, len(columns))
			results := make([]interface{}, len(columns))

			for i, _ := range columns {
				resultRefs[i] = &results[i]
			}

			if err := rows.Scan(resultRefs...); err != nil {
				fmt.Println(err)
				return
			}

			rowStrings := make([]string, len(columns))

			for i, _ := range columns {
				switch results[i].(type) {
				case int:
					rowStrings[i] = strconv.Itoa(results[i].(int))
				case int64:
					rowStrings[i] = strconv.FormatInt(results[i].(int64), 10)
				case nil:
					rowStrings[i] = ""
				default:
					rowStrings[i] = fmt.Sprintf("%s", results[i])
				}
			}

			writer.Write(rowStrings)
		}
		if err := rows.Err(); err != nil {
			fmt.Println(err)
			return
		}

		writer.Flush()
	}
}