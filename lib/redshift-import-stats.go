package mpredshiftimportstats

import (
	"errors"
	"fmt"
	"os"
	"strings"

	flags "github.com/jessevdk/go-flags"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	mp "github.com/mackerelio/go-mackerel-plugin"
	"github.com/mackerelio/golib/logging"
)

const (
	DEFAULT_OFFSET           = "24"
	QuerySourceTimeStampDiff = `	(
		SELECT EXTRACT(epoch FROM CONVERT_TIMEZONE('JST', SYSDATE) - MAX(%[2]s)) AS %[4]s_delay
		FROM %[1]s WHERE %[2]s >= CONVERT_TIMEZONE('JST', DATEADD(hour, -%[3]s, SYSDATE))
	) AS %[4]s`

	QuerySourceIntegerDiff = `	(
		SELECT EXTRACT(epoch FROM CONVERT_TIMEZONE('JST', SYSDATE)) - MAX(%[2]s) AS %[4]s_delay
		FROM %[1]s WHERE %[2]s >= EXTRACT(epoch FROM CONVERT_TIMEZONE('JST', DATEADD(hour, -%[3]s, SYSDATE)))
	) AS %[4]s`
)

var logger = logging.GetLogger("metrics.plugin.redshift-import-stats")

type RedshiftImportStats struct {
	Host       string   `short:"H" long:"host" value-name:"hostname" description:"Redshift endpoint" required:"true"`
	Database   string   `short:"d" long:"database" value-name:"database-name" description:"Database name" requierd:"true"`
	Port       string   `short:"p" long:"port" value-name:"5439" default:"5439" description:"Port number"`
	Username   string   `short:"u" long:"user" value-name:"root" description:"user name" required:"true"`
	Password   string   `short:"P" long:"password" value-name:"password" description:"password" required:"true"`
	OptTargets []string `short:"t" long:"target" required:"true" value-name:"table_name:column_name:column_type:offset" description:"Describe the target table, time column, and time column type with colon separated value."`
	Prefix     string   `long:"prefix"`
	Tempfile   string   `long:"tempfile"`
	Targets    []Target
}

type Target struct {
	Table  string
	Column string
	Type   string
	Offset string
}

func (t Target) SubQuery(querySource string) string {
	return fmt.Sprintf(querySource, t.Table, t.Column, t.Offset, t.TableAlias())
}

func (t Target) TableAlias() string {
	return strings.Replace(t.Table, ".", "_", -1)
}

func (t Target) ResultField() string {
	return fmt.Sprintf("%[1]s_delay", t.TableAlias())
}

func subQueryBuilder(querySource string, target Target) string {
	return target.SubQuery(querySource)
}

func (p *RedshiftImportStats) parseOptTarget() error {
	for _, t := range p.OptTargets {
		v := strings.Split(t, ":")
		if len(v) < 3 {
			return errors.New(fmt.Sprintf("Can't parse target: %s, must be table:column:type(:offset) format.", t))
		}

		if v[2] != "timestamp" && v[2] != "integer" {
			return errors.New(fmt.Sprintf("Invalid type: %s, target: %s", v[2], t))
		}

		offset := DEFAULT_OFFSET
		if len(v) == 4 {
			offset = v[3]
		}

		p.Targets = append(p.Targets, Target{Table: v[0], Column: v[1], Type: v[2], Offset: offset})
	}
	return nil
}

func QueryBuilder(stats *RedshiftImportStats) string {
	out := &strings.Builder{}

	subQueries := []string{}
	for _, t := range stats.Targets {
		qs := ""
		switch t.Type {
		case "integer":
			qs = QuerySourceIntegerDiff
		default:
			qs = QuerySourceTimeStampDiff
		}
		subQueries = append(subQueries, subQueryBuilder(qs, t))
	}

	fmt.Fprintln(out, "SELECT")

	fmt.Fprintln(
		out,
		strings.Join(
			func() []string {
				r := []string{}
				for _, t := range stats.Targets {
					r = append(r, "\t"+t.TableAlias()+"."+t.ResultField())
				}
				return r
			}(),
			",\n",
		),
	)

	fmt.Fprintln(out, "FROM")

	fmt.Fprintln(out, strings.Join(subQueries, ",\n")+";")

	return out.String()
}

func (t Target) MetricDef() mp.Metrics {
	return mp.Metrics{
		Name:  t.ResultField(),
		Label: strings.Title(strings.Replace(t.ResultField(), "_", " ", -1)),
		Diff:  false,
	}
}

func (p *RedshiftImportStats) MetricKeyPrefix() string {
	if p.Prefix == "" {
		p.Prefix = "redshift-import-stats"
	}
	return p.Prefix
}

func (p *RedshiftImportStats) GraphDefinition() map[string]mp.Graphs {
	labelPrefix := strings.Title(p.Prefix)

	delayMetrics := []mp.Metrics{}

	for _, t := range p.Targets {
		delayMetrics = append(delayMetrics, t.MetricDef())
	}

	var graphdef = map[string]mp.Graphs{
		"delay": {
			Label:   labelPrefix + " Delay",
			Unit:    mp.UnitInteger,
			Metrics: delayMetrics,
		},
	}

	return graphdef
}

func (p *RedshiftImportStats) FetchMetrics() (map[string]float64, error) {
	resultMap := map[string]interface{}{}
	metrics := map[string]float64{}

	db, err := sqlx.Connect("postgres", fmt.Sprintf("host=%s port=%s user=%s dbname=%s sslmode=disable", p.Host, p.Port, p.Username, p.Database))
	if err != nil {
		logger.Errorf("Failed FetchMetrics: Connect Redshift: %s", err)
		return metrics, err
	}

	row := db.QueryRowx(QueryBuilder(p))

	if err := row.MapScan(resultMap); err != nil {
		logger.Errorf("Failed FetchMetrics: MapScan: %s", err)
		return metrics, err
	}

	for k, v := range resultMap {
		metric := 0.0
		if f64, ok := v.(float64); ok {
			metric = f64
		} else if i64, ok := v.(int64); ok {
			metric = float64(i64)
		}
		metrics[k] = metric
	}

	return metrics, nil
}

func Do() {
	stats := &RedshiftImportStats{
		Targets: []Target{},
	}

	parser := flags.NewParser(stats, flags.PassDoubleDash|flags.HelpFlag)

	_, err := parser.ParseArgs(os.Args)
	if err != nil {
		if flagsErr, ok := err.(*flags.Error); ok && flagsErr.Type == flags.ErrHelp {
			fmt.Println(flagsErr)
			return
		}
		fmt.Println(err)
		fmt.Println()
		parser.WriteHelp(os.Stdout)
		os.Exit(3)
		return
	}

	if len(stats.OptTargets) == 0 {
		fmt.Println("target empty. See help (option: --help)")
		os.Exit(3)
		return
	}

	if err := stats.parseOptTarget(); err != nil {
		fmt.Printf("Failed Parse Targets: %s\n", err)
		os.Exit(3)
		return
	}

	helper := mp.NewMackerelPlugin(stats)
	helper.Tempfile = stats.Tempfile
	helper.Run()
}
