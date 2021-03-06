package test

import (
	"testing"
	"fmt"
	"time"
	"github.com/gruntwork-io/terratest/modules/http-helper"
	"github.com/gruntwork-io/terratest/modules/random"
	"github.com/gruntwork-io/terratest/modules/aws"
	"github.com/gruntwork-io/terratest/modules/test-structure"
	"github.com/gruntwork-io/terratest/modules/packer"
	"github.com/gruntwork-io/terratest/modules/terraform"
	"github.com/gruntwork-io/terratest/modules/logger"
)

// This is a complicated, end-to-end integration test. It builds the AMI from examples/packer-docker-example,
// deploys it using the Terraform code on examples/terraform-packer-example, and checks that the web server in the AMI
// response to requests. The test is broken into "stages" so you can skip stages by setting environment variables (e.g.,
// skip stage "build_ami" by setting the environment variable "SKIP_build_ami=true"), which speeds up iteration when
// running this test over and over again locally.
func TestTerraformPackerExample(t *testing.T)  {
	t.Parallel()

	// Pick a random AWS region to test in. This helps ensure your code works in all regions.
	awsRegion := aws.GetRandomRegion(t, nil, nil)

	// The folder where we have our Terraform code
	workingDir := "../examples/terraform-packer-example"

	// Build the AMI for the web app
	test_structure.RunTestStage(t, "build_ami", func() {
		buildAmi(t, awsRegion, workingDir)
	})

	// At the end of the test, delete the AMI
	defer test_structure.RunTestStage(t, "cleanup_ami", func() {
		deleteAmi(t, awsRegion, workingDir)
	})

	// At the end of the test, undeploy the web app using Terraform
	defer test_structure.RunTestStage(t, "cleanup_terraform", func() {
		undeployUsingTerraform(t, workingDir)
	})

	// At the end of the test, fetch the most recent syslog entries from each Instance. This can be useful for
	// debugging issues without having to manually SSH to the server.
	defer test_structure.RunTestStage(t, "logs", func() {
		fetchSyslogForInstance(t, awsRegion, workingDir)
	})

	// Deploy the web app using Terraform
	test_structure.RunTestStage(t, "deploy_terraform", func() {
		deployUsingTerraform(t, awsRegion, workingDir)
	})

	// Validate that the web app deployed and is responding to HTTP requests
	test_structure.RunTestStage(t, "validate", func() {
		validateInstanceRunningWebServer(t, workingDir)
	})
}

// Build the AMI in packer-docker-example
func buildAmi(t *testing.T, awsRegion string, workingDir string) {
	packerOptions := &packer.Options {
		// The path to where the Packer template is located
		Template: "../examples/packer-docker-example/build.json",

		// Only build the AMI
		Only: "ubuntu-ami",

		// Variables to pass to our Packer build using -var options
		Vars: map[string]string {
			"aws_region": awsRegion,
		},
	}

	// Save the Packer Options so future test stages can use them
	test_structure.SavePackerOptions(t, workingDir, packerOptions)

	// Build the AMI
	amiId := packer.BuildAmi(t, packerOptions)

	// Save the AMI ID so future test stages can use them
	test_structure.SaveAmiId(t, workingDir, amiId)
}

// Delete the AMI
func deleteAmi(t *testing.T, awsRegion string, workingDir string) {
	// Load the AMI ID and Packer Options saved by the earlier build_ami stage
	amiId := test_structure.LoadAmiId(t, workingDir)

	aws.DeleteAmi(t, awsRegion, amiId)
}

// Deploy the terraform-packer-example using Terraform
func deployUsingTerraform(t *testing.T, awsRegion string, workingDir string) {
	// A unique ID we can use to namespace resources so we don't clash with anything already in the AWS account or
	// tests running in parallel
	uniqueId := random.UniqueId()

	// Give this EC2 Instance and other resources in the Terraform code a name with a unique ID so it doesn't clash
	// with anything else in the AWS account.
	instanceName := fmt.Sprintf("terratest-http-example-%s", uniqueId)

	// Specify the text the EC2 Instance will return when we make HTTP requests to it.
	instanceText := fmt.Sprintf("Hello, %s!", uniqueId)

	// Load the AMI ID saved by the earlier build_ami stage
	amiId := test_structure.LoadAmiId(t, workingDir)

	terraformOptions := &terraform.Options {
		// The path to where our Terraform code is located
		TerraformDir: workingDir,

		// Variables to pass to our Terraform code using -var options
		Vars: map[string]interface{} {
			"aws_region":    awsRegion,
			"instance_name": instanceName,
			"instance_text": instanceText,
			"ami_id":        amiId,
		},
	}

	// Save the Terraform Options struct, instance name, and instance text so future test stages can use it
	test_structure.SaveTerraformOptions(t, workingDir, terraformOptions)

	// This will run `terraform init` and `terraform apply` and fail the test if there are any errors
	terraform.InitAndApply(t, terraformOptions)
}

// Undeploy the terraform-packer-example using Terraform
func undeployUsingTerraform(t *testing.T, workingDir string) {
	// Load the Terraform Options saved by the earlier deploy_terraform stage
	terraformOptions := test_structure.LoadTerraformOptions(t, workingDir)

	terraform.Destroy(t, terraformOptions)
}

// Fetch the most recent syslogs for the instance. This is a handy way to see what happened on the Instance as part of
// your test log output, without having to re-run the test and manually SSH to the Instance.
func fetchSyslogForInstance(t *testing.T, awsRegion string, workingDir string) {
	// Load the Terraform Options saved by the earlier deploy_terraform stage
	terraformOptions := test_structure.LoadTerraformOptions(t, workingDir)

	instanceId := terraform.OutputRequired(t, terraformOptions, "instance_id")
	logs := aws.GetSyslogForInstance(t, instanceId, awsRegion)

	logger.Logf(t, "Most recent syslog for Instance %s:\n\n%s\n", instanceId, logs)
}

// Validate the web server has been deployed and is working
func validateInstanceRunningWebServer(t *testing.T, workingDir string) {
	// Load the Terraform Options saved by the earlier deploy_terraform stage
	terraformOptions := test_structure.LoadTerraformOptions(t, workingDir)

	// Run `terraform output` to get the value of an output variable
	instanceUrl := terraform.Output(t, terraformOptions, "instance_url")

	// Figure out what text the instance should return for each request
	instanceText := terraformOptions.Vars["instance_text"].(string)

	// It can take a minute or so for the Instance to boot up, so retry a few times
	maxRetries := 15
	timeBetweenRetries := 5 * time.Second

	// Verify that we get back a 200 OK with the expected instanceText
	http_helper.HttpGetWithRetry(t, instanceUrl, 200, instanceText, maxRetries, timeBetweenRetries)
}