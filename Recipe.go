//go:build gobake
package bake_recipe

import (
	"fmt"
	"github.com/fezcode/gobake"
)

func Run(bake *gobake.Engine) error {
	if err := bake.LoadRecipeInfo("recipe.piml"); err != nil {
		return err
	}

	bake.Task("build", "Builds for Windows, Linux, and macOS (amd64 + arm64 each)", func(ctx *gobake.Context) error {
		ctx.Log("Building %s v%s...", bake.Info.Name, bake.Info.Version)
		if err := ctx.Mkdir("build"); err != nil {
			return err
		}
		targets := []struct {
			os   string
			arch string
		}{
			{"windows", "amd64"},
			{"windows", "arm64"},
			{"linux", "amd64"},
			{"linux", "arm64"},
			{"darwin", "amd64"},
			{"darwin", "arm64"},
		}
		ldflags := fmt.Sprintf("-X main.Version=%s", bake.Info.Version)
		for _, t := range targets {
			output := "build/" + bake.Info.Name + "-" + t.os + "-" + t.arch
			if t.os == "windows" {
				output += ".exe"
			}
			ctx.Env = []string{
				"CGO_ENABLED=0",
				"GOOS=" + t.os,
				"GOARCH=" + t.arch,
			}
			ctx.Log("  -> %s/%s", t.os, t.arch)
			if err := ctx.Run("go", "build", "-ldflags", ldflags, "-o", output, "."); err != nil {
				return err
			}
		}
		return nil
	})

	bake.Task("clean", "Removes build artifacts", func(ctx *gobake.Context) error {
		return ctx.Remove("build")
	})

	return nil
}
