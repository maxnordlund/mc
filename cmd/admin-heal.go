/*
 * MinIO Client (C) 2017, 2018 MinIO, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package cmd

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/minio/cli"
	json "github.com/minio/mc/pkg/colorjson"
	"github.com/minio/mc/pkg/console"
	"github.com/minio/mc/pkg/probe"
	"github.com/minio/minio/pkg/madmin"
)

const (
	scanNormalMode = "normal"
	scanDeepMode   = "deep"
)

var adminHealFlags = []cli.Flag{
	cli.StringFlag{
		Name:  "scan",
		Usage: "select the healing scan mode (normal/deep)",
		Value: scanNormalMode,
	},
	cli.BoolFlag{
		Name:  "recursive, r",
		Usage: "heal recursively",
	},
	cli.BoolFlag{
		Name:  "dry-run, n",
		Usage: "only inspect data, but do not mutate",
	},
	cli.BoolFlag{
		Name:  "force-start, f",
		Usage: "force start a new heal sequence",
	},
	cli.BoolFlag{
		Name:  "force-stop, s",
		Usage: "Force stop a running heal sequence",
	},
	cli.BoolFlag{
		Name:  "remove",
		Usage: "remove dangling objects in heal sequence",
	},
}

var adminHealCmd = cli.Command{
	Name:            "heal",
	Usage:           "heal disks, buckets and objects on MinIO server",
	Action:          mainAdminHeal,
	Before:          setGlobalsFromContext,
	Flags:           append(adminHealFlags, globalFlags...),
	HideHelpCommand: true,
	CustomHelpTemplate: `NAME:
  {{.HelpName}} - {{.Usage}}

USAGE:
  {{.HelpName}} [FLAGS] TARGET

FLAGS:
  {{range .VisibleFlags}}{{.}}
  {{end}}

SCAN MODES:
   normal (default): Heal objects which are missing on one or more disks.
   deep            : Heal objects which are missing on one or more disks. Also heal objects with silent data corruption.

EXAMPLES:
    1. To format newly replaced disks in a MinIO server with alias 'myminio'
       $ {{.HelpName}} myminio

    2. Heal 'testbucket' in a MinIO server with alias 'myminio'
       $ {{.HelpName}} myminio/testbucket/

    3. Heal all objects under 'dir' prefix
       $ {{.HelpName}} --recursive myminio/testbucket/dir/

    4. Issue a dry-run heal operation to inspect objects health but not heal them
       $ {{.HelpName}} --dry-run myminio

    5. Issue a dry-run heal operation to inspect objects health under 'dir' prefix
       $ {{.HelpName}} --recursive --dry-run myminio/testbucket/dir/

    6. Force start a running heal sequence (meaning it will force kill the running heal sequence and start a new one)
       $ {{.HelpName}} --force-start myminio/testbucket/dir/
		
    7. Force stop a running heal sequence (meaning it will force kill the running heal sequence)
       $ {{.HelpName}} --force-stop myminio/testbucket/dir/
		
    8. Issue a dry-run heal operation to inspect objects health under 'dir' prefix
       $ {{.HelpName}} --dry-run myminio/testbucket/dir/
`,
}

func checkAdminHealSyntax(ctx *cli.Context) {
	if len(ctx.Args()) != 1 {
		cli.ShowCommandHelpAndExit(ctx, "heal", 1) // last argument is exit code
	}

	// Check for scan argument
	scanArg := ctx.String("scan")
	scanArg = strings.ToLower(scanArg)
	if scanArg != scanNormalMode && scanArg != scanDeepMode {
		cli.ShowCommandHelpAndExit(ctx, "heal", 1) // last argument is exit code
	}
}

// stopHealMessage is container for stop heal success and failure messages.
type stopHealMessage struct {
	Status string `json:"status"`
	Alias  string `json:"alias"`
}

// String colorized stop heal message.
func (s stopHealMessage) String() string {
	return console.Colorize("HealStopped", "Heal stopped successfully at `"+s.Alias+"`.")
}

// JSON jsonified stop heal message.
func (s stopHealMessage) JSON() string {
	stopHealJSONBytes, e := json.MarshalIndent(s, "", " ")
	fatalIf(probe.NewError(e), "Unable to marshal into JSON.")

	return string(stopHealJSONBytes)
}

// backgroundHealStatusMessage is container for stop heal success and failure messages.
type backgroundHealStatusMessage struct {
	Status   string `json:"status"`
	HealInfo madmin.BgHealState
}

// String colorized to show background heal status message.
func (s backgroundHealStatusMessage) String() string {
	healPrettyMsg := console.Colorize("HealBackgroundTitle", "Background healing status:\n")
	healPrettyMsg += fmt.Sprintf("  Total items scanned: %s\n",
		console.Colorize("HealBackground", s.HealInfo.ScannedItemsCount))
	healPrettyMsg += fmt.Sprintf("  Last background heal check: %s\n",
		console.Colorize("HealBackground", timeDurationToHumanizedDuration(time.Since(s.HealInfo.LastHealActivity)).String()+" ago"))
	return healPrettyMsg
}

// JSON jsonified stop heal message.
func (s backgroundHealStatusMessage) JSON() string {
	healJSONBytes, e := json.MarshalIndent(s, "", " ")
	fatalIf(probe.NewError(e), "Unable to marshal into JSON.")

	return string(healJSONBytes)
}

func transformScanArg(scanArg string) madmin.HealScanMode {
	switch scanArg {
	case "deep":
		return madmin.HealDeepScan
	}
	return madmin.HealNormalScan
}

// mainAdminHeal - the entry function of heal command
func mainAdminHeal(ctx *cli.Context) error {

	// Check for command syntax
	checkAdminHealSyntax(ctx)

	// Get the alias parameter from cli
	args := ctx.Args()
	aliasedURL := args.Get(0)

	console.SetColor("Heal", color.New(color.FgGreen, color.Bold))
	console.SetColor("HealBackgroundTitle", color.New(color.FgGreen, color.Bold))
	console.SetColor("HealBackground", color.New(color.Bold))
	console.SetColor("HealUpdateUI", color.New(color.FgYellow, color.Bold))
	console.SetColor("HealStopped", color.New(color.FgGreen, color.Bold))

	// Create a new MinIO Admin Client
	client, err := newAdminClient(aliasedURL)
	if err != nil {
		fatalIf(err.Trace(aliasedURL), "Cannot initialize admin client.")
		return nil
	}

	// Compute bucket and object from the aliased URL
	aliasedURL = filepath.ToSlash(aliasedURL)
	splits := splitStr(aliasedURL, "/", 3)
	bucket, prefix := splits[1], splits[2]

	// Return the background heal status when the user
	// doesn't pass a bucket or --recursive flag.
	if bucket == "" && !ctx.Bool("recursive") {
		bgHealStatus, berr := client.BackgroundHealStatus()
		fatalIf(probe.NewError(berr), "Failed to get the status of the background heal.")
		printMsg(backgroundHealStatusMessage{Status: "success", HealInfo: bgHealStatus})
		return nil
	}

	opts := madmin.HealOpts{
		ScanMode:  transformScanArg(ctx.String("scan")),
		Remove:    ctx.Bool("remove"),
		Recursive: ctx.Bool("recursive"),
		DryRun:    ctx.Bool("dry-run"),
	}

	forceStart := ctx.Bool("force-start")
	forceStop := ctx.Bool("force-stop")
	if forceStop {
		_, _, herr := client.Heal(bucket, prefix, opts, "", forceStart, forceStop)
		fatalIf(probe.NewError(herr), "Failed to stop heal sequence.")
		printMsg(stopHealMessage{Status: "success", Alias: aliasedURL})
		return nil
	}

	healStart, _, herr := client.Heal(bucket, prefix, opts, "", forceStart, false)
	fatalIf(probe.NewError(herr), "Failed to start heal sequence.")

	ui := uiData{
		Bucket:                bucket,
		Prefix:                prefix,
		Client:                client,
		ClientToken:           healStart.ClientToken,
		ForceStart:            forceStart,
		HealOpts:              &opts,
		ObjectsByOnlineDrives: make(map[int]int64),
		HealthCols:            make(map[col]int64),
		CurChan:               cursorAnimate(),
	}

	res, e := ui.DisplayAndFollowHealStatus(aliasedURL)
	if e != nil {
		if res.FailureDetail != "" {
			data, _ := json.MarshalIndent(res, "", " ")
			traceStr := string(data)
			fatalIf(probe.NewError(e).Trace(aliasedURL, traceStr), "Unable to display heal status.")
		} else {
			fatalIf(probe.NewError(e).Trace(aliasedURL), "Unable to display heal status.")
		}
	}
	return nil
}
