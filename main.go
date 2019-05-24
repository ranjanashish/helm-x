package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mumoshu/helm-x/pkg"
	"github.com/spf13/pflag"
	"io"
	"log"
	"os"

	"github.com/spf13/cobra"
	"k8s.io/klog"

	"gopkg.in/yaml.v3"
)

var Version string

func main() {
	klog.InitFlags(nil)

	cmd := NewRootCmd()
	if err := cmd.Execute(); err != nil {
		log.Fatal("Failed to execute command")
	}
}

func NewRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "helm-x [apply|diff|template|dump|adopt]",
		Short:   "Turn Kubernetes manifests, Kustomization, Helm Chart into Helm release. Sidecar injection supported.",
		Long:    ``,
		Version: Version,
	}

	out := cmd.OutOrStdout()

	cmd.AddCommand(NewApplyCommand(out, "apply", true))
	cmd.AddCommand(NewApplyCommand(out, "upgrade", false))
	cmd.AddCommand(NewDiffCommand(out))
	cmd.AddCommand(NewTemplateCommand(out))
	cmd.AddCommand(NewUtilDumpRelease(out))
	cmd.AddCommand(NewAdopt(out))

	return cmd
}

type dumpCmd struct {
	*x.ClientOpts

	TillerNamespace string

	Out io.Writer
}

// NewApplyCommand represents the apply command
func NewApplyCommand(out io.Writer, cmdName string, installByDefault bool) *cobra.Command {
	upOpts := &x.UpgradeOpts{Out: out}

	cmd := &cobra.Command{
		Use:   fmt.Sprintf("%s [RELEASE] [DIR_OR_CHART]", cmdName),
		Short: "Install or upgrade the helm release from the directory or the chart specified",
		Long: `Install or upgrade the helm release from the directory or the chart specified

Under the hood, this generates Kubernetes manifests from (1)directory containing manifests/kustomization/local helm chart or (2)remote helm chart, then inject sidecars, and finally install the result as a Helm release

When DIR_OR_CHART is a local helm chart, this copies it into a temporary directory, renders all the templates into manifests by running "helm template", and then run injectors to update manifests, and install the temporary chart by running "helm upgrade --install".

It's better than installing it with "kubectl apply -f", as you can leverage various helm sub-commands like "helm test" if you included tests in the "templates/tests" directory of the chart.
It's also better in regard to security and reproducibility, as creating a helm release allows helm to detect Kubernetes resources removed from the desired state but still exist in the cluster, and automatically delete unnecessary resources.

When DIR_OR_CHART is a local directory containing Kubernetes manifests, this copies all the manifests into a temporary directory, and turns it into a local Helm chart by generating a Chart.yaml whose version and appVersion are set to the value of the --version flag.

When DIR_OR_CHART contains kustomization.yaml, this runs "kustomize build" to generate manifests, and then run injectors to update manifests, and install the temporary chart by running "helm upgrade --install".
`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return errors.New("requires two arguments")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			release := args[0]
			dir := args[1]

			upOpts.ReleaseName = release
			tempDir, err := x.Chartify(dir, *upOpts.ChartifyOpts)
			if err != nil {
				cmd.SilenceUsage = true
				return err
			}

			if !upOpts.Debug {
				defer os.RemoveAll(tempDir)
			} else {
				klog.Infof("helm chart has been written to %s for you to see. please remove it afterwards", tempDir)
			}

			upOpts.Chart = tempDir

			if len(upOpts.Adopt) > 0 {
				if err := x.Adopt(upOpts.TillerNamespace, release, upOpts.Namespace, upOpts.Adopt); err != nil {
					return err
				}
			}

			if err := x.Upgrade(*upOpts); err != nil {
				cmd.SilenceUsage = true
				return err
			}

			return nil
		},
	}
	f := cmd.Flags()

	upOpts.ChartifyOpts = chartifyOptsFromFlags(f)
	upOpts.ClientOpts = clientOptsFromFlags(f)

	//f.StringVar(&u.release, "name", "", "release name (default \"release-name\")")
	f.IntVar(&upOpts.Timeout, "timeout", 300, "time in seconds to wait for any individual Kubernetes operation (like Jobs for hooks)")

	f.BoolVar(&upOpts.DryRun, "dry-run", false, "simulate an upgrade")

	f.BoolVar(&upOpts.Install, "install", installByDefault, "install the release if missing")

	f.StringSliceVarP(&upOpts.Adopt, "adopt", "", []string{}, "adopt existing k8s resources before apply")

	return cmd
}

// NewTemplateCommand represents the template command
func NewTemplateCommand(out io.Writer) *cobra.Command {
	templateOpts := &x.TemplateOpts{Out: out}

	cmd := &cobra.Command{
		Use:   "template [DIR_OR_CHART]",
		Short: "Print Kubernetes manifests that would be generated by `helm x apply`",
		Long: `Print Kubernetes manifests that would be generated by ` + "`helm x apply`" + `

Under the hood, this generates Kubernetes manifests from (1)directory containing manifests/kustomization/local helm chart or (2)remote helm chart, then inject sidecars, and finally print the resulting manifests

When DIR_OR_CHART is a local helm chart, this copies it into a temporary directory, renders all the templates into manifests by running "helm template", and then run injectors to update manifests, and prints the results.

When DIR_OR_CHART is a local directory containing Kubernetes manifests, this copies all the manifests into a temporary directory, and turns it into a local Helm chart by generating a Chart.yaml whose version and appVersion are set to the value of the --version flag.

When DIR_OR_CHART contains kustomization.yaml, this runs "kustomize build" to generate manifests, and then run injectors to update manifests, and prints the results.
`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return errors.New("requires one argument")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]

			tempDir, err := x.Chartify(dir, *templateOpts.ChartifyOpts)
			if err != nil {
				cmd.SilenceUsage = true
				return err
			}

			if !templateOpts.Debug {
				klog.Infof("helm chart has been written to %s for you to see. please remove it afterwards", tempDir)
				defer os.RemoveAll(tempDir)
			}

			if err := x.Template(tempDir, *templateOpts); err != nil {
				cmd.SilenceUsage = true
				return err
			}

			return nil
		},
	}
	f := cmd.Flags()

	templateOpts.ChartifyOpts = chartifyOptsFromFlags(f)

	f.StringVar(&templateOpts.ReleaseName, "name", "release-name", "release name (default \"release-name\")")
	f.StringVar(&templateOpts.TillerNamespace, "tiller-namsepace", "kube-system", "Namespace in which release confgimap/secret objects reside")
	f.BoolVar(&templateOpts.IncludeReleaseConfigmap, "include-release-configmap", false, "turn the result into a proper helm release, by removing hooks from the manifest, and including a helm release configmap/secret that should otherwise created by \"helm [upgrade|install]\"")
	f.BoolVar(&templateOpts.IncludeReleaseSecret, "include-release-secret", false, "turn the result into a proper helm release, by removing hooks from the manifest, and including a helm release configmap/secret that should otherwise created by \"helm [upgrade|install]\"")

	return cmd
}

// NewDiffCommand represents the diff command
func NewDiffCommand(out io.Writer) *cobra.Command {
	diffOpts := &x.DiffOpts{Out: out}

	cmd := &cobra.Command{
		Use:   "diff [RELEASE] [DIR_OR_CHART]",
		Short: "Show a diff explaining what `helm x apply` would change",
		Long: `Show a diff explaining what ` + "`helm x apply`" + ` would change.

Under the hood, this generates Kubernetes manifests from (1)directory containing manifests/kustomization/local helm chart or (2)remote helm chart, then inject sidecars, and finally print the resulting manifests

When DIR_OR_CHART is a local helm chart, this copies it into a temporary directory, renders all the templates into manifests by running "helm template", and then run injectors to update manifests, and prints the results.

When DIR_OR_CHART is a local directory containing Kubernetes manifests, this copies all the manifests into a temporary directory, and turns it into a local Helm chart by generating a Chart.yaml whose version and appVersion are set to the value of the --version flag.

When DIR_OR_CHART contains kustomization.yaml, this runs "kustomize build" to generate manifests, and then run injectors to update manifests, and prints the results.
`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return errors.New("requires two arguments")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			release := args[0]
			dir := args[1]

			diffOpts.ReleaseName = release
			tempDir, err := x.Chartify(dir, *diffOpts.ChartifyOpts)
			if err != nil {
				cmd.SilenceUsage = true
				return err
			}

			if !diffOpts.Debug {
				klog.Infof("helm chart has been written to %s for you to see. please remove it afterwards", tempDir)
				defer os.RemoveAll(tempDir)
			}

			diffOpts.Chart = tempDir
			diffOpts.ReleaseName = release
			if err := x.Diff(*diffOpts); err != nil {
				cmd.SilenceUsage = true
				return err
			}

			return nil
		},
	}
	f := cmd.Flags()

	diffOpts.ChartifyOpts = chartifyOptsFromFlags(f)
	diffOpts.ClientOpts = clientOptsFromFlags(f)

	//f.StringVar(&u.release, "name", "", "release name (default \"release-name\")")

	return cmd
}

// NewAdopt represents the adopt command
func NewAdopt(out io.Writer) *cobra.Command {
	adoptOpts := &x.AdoptOpts{Out: out}

	cmd := &cobra.Command{
		Use: "adopt [RELEASE] [RESOURCES]...",
		Short: `Adopt the existing kubernetes resources as a helm release

RESOURCES are represented as a whitespace-separated list of kind/name, like:

  configmap/foo.v1 secret/bar deployment/myapp

So that the full command looks like:

  helm x adopt myrelease configmap/foo.v1 secret/bar deployment/myapp
`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 {
				return errors.New("requires at least two argument")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			release := args[0]
			tillerNs := adoptOpts.TillerNamespace
			resources := args[1:]

			return x.Adopt(tillerNs, release, adoptOpts.Namespace, resources)
		},
	}
	f := cmd.Flags()

	adoptOpts.ClientOpts = clientOptsFromFlags(f)

	f.StringVar(&adoptOpts.Namespace, "namespace", "", "The Namespace in which the resources to be adopted reside")

	return cmd
}

// NewDiffCommand represents the diff command
func NewUtilDumpRelease(out io.Writer) *cobra.Command {
	dumpOpts := &dumpCmd{Out: out}

	cmd := &cobra.Command{
		Use:   "dump [RELEASE]",
		Short: "Dump the release object for developing purpose",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return errors.New("requires one argument")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			release := args[0]
			storage, err := x.NewConfigMapsStorage(dumpOpts.TillerNamespace)
			if err != nil {
				return err
			}

			r, err := storage.GetRelease(release)
			if err != nil {
				return err
			}

			jsonBytes, err := json.Marshal(r)

			jsonObj := map[string]interface{}{}
			if err := json.Unmarshal(jsonBytes, &jsonObj); err != nil {
				return err
			}

			yamlBytes, err := yaml.Marshal(jsonObj)
			if err != nil {
				return err
			}

			fmt.Printf("%s\n", string(yamlBytes))

			fmt.Printf("manifest:\n%s", jsonObj["manifest"])

			return nil
		},
	}
	f := cmd.Flags()

	dumpOpts.ClientOpts = clientOptsFromFlags(f)

	return cmd
}

func chartifyOptsFromFlags(f *pflag.FlagSet) *x.ChartifyOpts {
	chartifyOpts := &x.ChartifyOpts{}

	f.StringArrayVar(&chartifyOpts.Injectors, "injector", []string{}, "DEPRECATED: Use `--inject \"CMD ARG1 ARG2\"` instead. injector to use (must be pre-installed) and flags to be passed in the syntax of `'CMD SUBCMD,FLAG1=VAL1,FLAG2=VAL2'`. Flags should be without leading \"--\" (can specify multiple). \"FILE\" in values are replaced with the Kubernetes manifest file being injected. Example: \"--injector 'istioctl kube-inject f=FILE,injectConfigFile=inject-config.yaml,meshConfigFile=mesh.config.yaml\"")
	f.StringArrayVar(&chartifyOpts.Injects, "inject", []string{}, "injector to use (must be pre-installed) and flags to be passed in the syntax of `'istioctl kube-inject -f FILE'`. \"FILE\" is replaced with the Kubernetes manifest file being injected")
	f.StringArrayVar(&chartifyOpts.AdhocChartDependencies, "adhoc-dependency", []string{}, "Adhoc dependencies to be added to the temporary local helm chart being installed. Syntax: ALIAS=REPO/CHART:VERSION e.g. mydb=stable/mysql:1.2.3")
	f.StringArrayVar(&chartifyOpts.JsonPatches, "json-patch", []string{}, "Kustomize JSON Patch file to be applied to the rendered K8s manifests. Allows customizing your chart without forking or updating")
	f.StringArrayVar(&chartifyOpts.StrategicMergePatches, "strategic-merge-patch", []string{}, "Kustomize Strategic Merge Patch file to be applied to the rendered K8s manifests. Allows customizing your chart without forking or updating")

	f.StringArrayVarP(&chartifyOpts.ValuesFiles, "values", "f", []string{}, "specify values in a YAML file or a URL (can specify multiple)")
	f.StringArrayVar(&chartifyOpts.SetValues, "set", []string{}, "set values on the command line (can specify multiple)")
	f.StringVar(&chartifyOpts.Namespace, "namespace", "", "Namespace to install the release into (only used if --install is set). Defaults to the current kube config Namespace")
	f.StringVar(&chartifyOpts.TillerNamespace, "tiller-namespace", "kube-system", "Namespace to in which release configmap/secret objects reside")
	f.StringVar(&chartifyOpts.ChartVersion, "version", "", "specify the exact chart version to use. If this is not specified, the latest version is used")

	f.BoolVar(&chartifyOpts.Debug, "debug", false, "enable verbose output")

	return chartifyOpts
}

func clientOptsFromFlags(f *pflag.FlagSet) *x.ClientOpts {
	clientOpts := &x.ClientOpts{}
	f.BoolVar(&clientOpts.TLS, "tls", false, "enable TLS for request")
	f.StringVar(&clientOpts.TLSCert, "tls-cert", "", "path to TLS certificate file (default: $HELM_HOME/cert.pem)")
	f.StringVar(&clientOpts.TLSKey, "tls-key", "", "path to TLS key file (default: $HELM_HOME/key.pem)")
	f.StringVar(&clientOpts.KubeContext, "kubecontext", "", "the kubeconfig context to use")
	return clientOpts
}
