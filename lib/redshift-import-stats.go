package mpredshiftimportstats

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	flags "github.com/jessevdk/go-flags"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	mp "github.com/mackerelio/go-mackerel-plugin"
	"github.com/mackerelio/golib/logging"
)

const (
	DEFAULT_OFFSET           = 24
	QuerySourceTimeStampDiff = `	(
		SELECT %[1]d - EXTRACT(epoch FROM MAX(%[3]s)) AS %[5]s_delay
		FROM %[2]s WHERE %[3]s >= '%[4]s'
	) AS %[5]s`

	QuerySourceIntegerDiff = `	(
		SELECT %[1]d - MAX(%[3]s) AS %[5]s_delay
		FROM %[2]s WHERE %[3]s >= %[4]d
	) AS %[5]s`
	Prefix = "redshift-import-stats"
)

var (
	logger = logging.GetLogger("metrics.plugin." + Prefix)

	now    time.Time
	nowUTC time.Time
)

type RedshiftImportStats struct {
	Host       string   `short:"H" long:"host" value-name:"hostname" description:"redshift endpoint"`
	Database   string   `short:"d" long:"database" value-name:"database-name" description:"database name"`
	Port       string   `short:"p" long:"port" value-name:"5439" default:"5439" description:"port number"`
	Username   string   `short:"u" long:"user" value-name:"root" description:"user name"`
	Password   string   `short:"P" long:"password" value-name:"password" description:"password"`
	OptTargets []string `short:"t" long:"target" required:"true" value-name:"table_name:column_name:column_type:offset" description:"Specify the target table (multiple allowed).\ntarget format: $1:$2:$3:$4\n$1: table_name: Target table name.\n$2: column_name: Time column of the table.\n$3: column_type: Type of the time column. [integer, timestamp, timestampz]\n$4: offset: Option. By default, narrow by the where clause up to 24 hours ago."`
	Prefix     string   `long:"prefix"`
	Tempfile   string   `long:"tempfile"`
	Targets    []Target
}

type Target struct {
	Table  string
	Column string
	Type   string
	Offset time.Duration
}

func (t Target) SubQuery() string {
	if t.Type == "timestamp" {
		return fmt.Sprintf(QuerySourceTimeStampDiff, nowUTC.Unix(), t.Table, t.Column, pq.FormatTimestamp(nowUTC.Add(t.Offset*-1)), t.TableAlias())
	} else if t.Type == "tiemstampz" {
		return fmt.Sprintf(QuerySourceTimeStampDiff, now.Unix(), t.Table, t.Column, pq.FormatTimestamp(now.Add(t.Offset*-1)), t.TableAlias())
	} else {
		return fmt.Sprintf(QuerySourceIntegerDiff, now.Unix(), t.Table, t.Column, now.Add(t.Offset*-1).Unix(), t.TableAlias())
	}
}

func (t Target) TableAlias() string {
	return strings.Replace(t.Table, ".", "_", -1)
}

func (t Target) ResultField() string {
	return fmt.Sprintf("%[1]s_delay", t.TableAlias())
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
			var err error

			offset, err = strconv.Atoi(v[3])
			if err != nil {
				return errors.New(fmt.Sprintf("Invalid offset: %s", offset))
			}
		}

		p.Targets = append(p.Targets, Target{Table: v[0], Column: v[1], Type: v[2], Offset: time.Duration(offset) * time.Hour})
	}
	return nil
}

func QueryBuilder(stats *RedshiftImportStats) string {
	out := &strings.Builder{}

	subQueries := []string{}
	for _, t := range stats.Targets {
		subQueries = append(subQueries, t.SubQuery())
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

func (p *RedshiftImportStats) MetricKeyPrefix() string {
	if p.Prefix == "" {
		p.Prefix = Prefix
	}
	return Prefix + "-" + p.Prefix
}

func (p *RedshiftImportStats) GraphDefinition() map[string]mp.Graphs {
	labelPrefix := strings.Title(p.Prefix)

	return map[string]mp.Graphs{
		"delay.#": {
			Label: labelPrefix + " Delay",
			Unit:  mp.UnitInteger,
			Metrics: []mp.Metrics{
				{Name: "delay", Label: "delay"},
			},
		},
	}
}

func (p *RedshiftImportStats) FetchMetrics() (map[string]float64, error) {
	resultMap := map[string]interface{}{}
	metrics := map[string]float64{}

	dataSource := fmt.Sprintf("host=%s port=%s sslmode=verify-full", p.Host, p.Port)
	if p.Username != "" {
		dataSource += " user=" + p.Username
	}
	if p.Database != "" {
		dataSource += " dbname=" + p.Database
	}
	if p.Password != "" {
		dataSource += " password=" + p.Password
	}

	db, err := sqlx.Connect("postgres", dataSource)
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
		metrics["delay."+strings.Replace(k, "_delay", ".delay", 1)] = metric
	}

	return metrics, nil
}

func Do() {
	now = time.Now()
	_, offsetTZ := now.Zone()
	nowUTC = now.Add(time.Duration(offsetTZ) * time.Second)

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
