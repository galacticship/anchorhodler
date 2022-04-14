package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/galacticship/anchorhodler/internal"
	"github.com/galacticship/terra"
	"github.com/galacticship/terra/cosmos"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var rootCmd = &cobra.Command{
	Use:   "anchorhodler",
	Short: "",
	Long:  ``,
	Run: func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		returnCode := make(chan int)
		terminated := make(chan interface{})
		go trapSignals(cancel, terminated, returnCode)

		if err := run(ctx, terminated); err != nil {
			log.Error().Err(err).Send()
			go func(returnCode chan<- int) { returnCode <- -2 }(returnCode)
		}
		os.Exit(<-returnCode)

	},
}

func trapSignals(cancel context.CancelFunc, terminated chan interface{}, returnCode chan int) {
	stopSignal := make(chan os.Signal)
	signal.Notify(stopSignal, syscall.SIGTERM, syscall.SIGINT)
	<-stopSignal
	cancel()
	select {
	case <-time.After(30 * time.Second):
		log.Warn().Msg("app shutdown sequence timed out")
		returnCode <- 1
	case <-terminated:
		returnCode <- 0
	}
}

func run(ctx context.Context, terminated chan<- interface{}) error {
	defer close(terminated)
	mnemonic := viper.GetString("mnemonic")
	checkPeriod := time.Duration(viper.GetInt("checkperiod")) * time.Second
	if mnemonic == "" {
		return errors.New("mnemonic cannot be empty")
	}
	querier := terra.NewQuerier(&http.Client{
		Timeout: 30 * time.Second,
	}, viper.GetString("lcdurl"))
	wallet, err := terra.NewWalletFromMnemonic(
		querier,
		mnemonic,
		0,
		0,
		terra.WithGasAdjustment(cosmos.NewDecFromIntWithPrec(cosmos.NewInt(15), 1)))
	if err != nil {
		return errors.New("initializing wallet")
	}
	hodler, err := internal.NewAnchorHodler(querier, wallet)
	if err != nil {
		return errors.New("initializing anchor hodler object")
	}

	t := time.NewTicker(checkPeriod)
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-t.C:
			err = hodler.CheckLtv(ctx, viper.GetFloat64("minltv"), viper.GetFloat64("maxltv"), viper.GetFloat64("targetltv"))
			if err != nil {
				log.Error().Err(err).Msg("checking ltv")
			}
		}
	}
	return nil
}

func Execute() {
	cobra.CheckErr(rootCmd.Execute())
}

func init() {
	cobra.OnInitialize(initlog, initConfig)
}

func initlog() {
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	log.Logger = zerolog.New(
		zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: "02-01-2006 15:04:05",
			FormatLevel: func(i interface{}) string {
				return strings.ToUpper(fmt.Sprintf("| %-6s|", i))
			},
			FormatFieldName: func(i interface{}) string {
				return fmt.Sprintf("%s:", i)
			},
			FormatFieldValue: func(i interface{}) string {
				return strings.ToUpper(fmt.Sprintf("%s", i))
			},
		},
	).With().Timestamp().Logger()
}

func initConfig() {
	viper.SetEnvPrefix("ANCHORHODLER")
	viper.SetDefault("minltv", 65)
	viper.SetDefault("maxltv", 85)
	viper.SetDefault("targetltv", 75)
	viper.SetDefault("lcdurl", "https://lcd.terra.dev")
	viper.SetDefault("checkperiod", 30)

	cobra.CheckErr(viper.BindEnv("mnemonic"))
	cobra.CheckErr(viper.BindEnv("minltv"))
	cobra.CheckErr(viper.BindEnv("maxltv"))
	cobra.CheckErr(viper.BindEnv("targetltv"))
	cobra.CheckErr(viper.BindEnv("lcdurl"))
	cobra.CheckErr(viper.BindEnv("checkperiod"))
	viper.AutomaticEnv()
}
