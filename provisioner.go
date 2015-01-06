package mount

import (
  "fmt"
  "time"
  "log"
  "os"
  "os/exec"

  "github.com/flynn/go-shlex"
  "github.com/mitchellh/packer/common"
  "github.com/mitchellh/packer/packer"
  "github.com/mitchellh/packer/packer/plugin"
)

type config struct {
  common.PackerConfig `mapstructure:",squash"`

  // The local path of the ISO to mount.
  Source string


  // The timeout for retrying to start the process. Until this timeout
  // is reached, if the provisioner can't start a process, it retries.
  // This can be set high to allow for reboots.
  RawStartRetryTimeout string `mapstructure:"start_retry_timeout"`

  startRetryTimeout time.Duration

  // The configuration template
  tpl *packer.ConfigTemplate
}

type Provisioner struct {
  config config
}

type ExecuteTemplate struct {
  BuildName string
  Source    string
}

// stdout/stderr wrapper
type CommandWriter struct {
  WriteFunc func(string)
}

func (w CommandWriter) Write(p []byte) (n int, err error) {
  w.WriteFunc(string(p))
  return len(p), nil
}

func (p *Provisioner) Prepare(raws ...interface{}) error {
  md, err := common.DecodeConfig(&p.config, raws...)
  if err != nil {
    return err
  }

  p.config.tpl, err = packer.NewConfigTemplate()
  if err != nil {
    return err
  }
  p.config.tpl.UserVars = p.config.PackerUserVars

  // Accumulate any errors
  errs := common.CheckUnusedConfig(md)

  templates := map[string]*string{
    "source": &p.config.Source,
  }

  for n, ptr := range templates {
    var err error
    *ptr, err = p.config.tpl.Process(*ptr, nil)
    if err != nil {
      errs = packer.MultiErrorAppend(
        errs, fmt.Errorf("Error processing %s: %s", n, err))
    }
  }

  if p.config.Source != "" {
    if _, err := os.Stat(p.config.Source); err != nil {
      errs = packer.MultiErrorAppend(errs,
        fmt.Errorf("Bad source '%s': %s", p.config.Source, err))
    }
  }

  if p.config.RawStartRetryTimeout != "" {
    p.config.startRetryTimeout, err = time.ParseDuration(p.config.RawStartRetryTimeout)
    if err != nil {
      errs = packer.MultiErrorAppend(
        errs, fmt.Errorf("Failed parsing start_retry_timeout: %s", err))
    }
  }

  if errs != nil && len(errs.Errors) > 0 {
    return errs
  }

  return nil
}

func (p *Provisioner) Provision(ui packer.Ui, comm packer.Communicator) error {
  if p.config.Source == "" {
    ui.Say("Unmounting")
    p.config.Source = "emptydrive"
  } else {
    ui.Say(fmt.Sprintf("Mounting %s", p.config.Source))
    _, err := os.Stat(p.config.Source)
    if err != nil {
      return err
    }
  }
  
  // p.config.PackerBuilderType
  executeCommand := "VBoxManage storageattach '{{.BuildName}}' --storagectl 'IDE Controller' --port 1 --device 0 --type dvddrive --medium '{{.Source}}'"

  command, err := p.config.tpl.Process(executeCommand,  &ExecuteTemplate{
    BuildName: p.config.PackerBuildName,
    Source:    p.config.Source,
  })

  if err != nil {
    return fmt.Errorf("Error processing command '%s': %s", executeCommand, err)
  }

  parts, err := shlex.Split(command)

  commandRunner := exec.Command(parts[0], parts[1:]...)

  commandRunner.Stdout = CommandWriter{ WriteFunc: ui.Say   }
  commandRunner.Stderr = CommandWriter{ WriteFunc: ui.Error }

  if err := commandRunner.Run(); err != nil {
    return fmt.Errorf("Error running command '%s': %s", executeCommand, err)
  }

  return nil
}

func (p *Provisioner) Cancel() {
  // Just hard quit. It isn't a big deal if what we're doing keeps
  // running on the other side.
  os.Exit(0)
}

// retryable will retry the given function over and over until a
// non-error is returned.
func (p *Provisioner) retryable(f func() error) error {
  startTimeout := time.After(p.config.startRetryTimeout)
  for {
    var err error
    if err = f(); err == nil {
      return nil
    }

    // Create an error and log it
    err = fmt.Errorf("Retryable error: %s", err)
    log.Printf(err.Error())

    // Check if we timed out, otherwise we retry. It is safe to
    // retry since the only error case above is if the command
    // failed to START.
    select {
    case <-startTimeout:
      return err
    default:
      time.Sleep(2 * time.Second)
    }
  }
}

func main() {
  server, err := plugin.Server()
  if err != nil {
    panic(err)
  }

  server.RegisterProvisioner(new(Provisioner))
  server.Serve()
}
