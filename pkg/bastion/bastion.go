package bastion

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2Types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdsTypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/aws/smithy-go"
)

//go:embed stack.json
var raw []byte

type Bastion struct {
	DBS     []rdsTypes.DBInstance
	Task    *ecsTypes.Task
	Outputs map[string]string
}

func New() (*Bastion, error) {
	result := &Bastion{
		Outputs: map[string]string{},
	}
	var template map[string]interface{}
	err := json.Unmarshal(raw, &template)
	if err != nil {
		return nil, err
	}

	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		return nil, err
	}

	cfClient := cloudformation.NewFromConfig(cfg)
	ec2Client := ec2.NewFromConfig(cfg)
	rdsClient := rds.NewFromConfig(cfg)

	var vpcs *ec2.DescribeVpcsOutput
	var stack *types.Stack
	errorCh := make(chan error, 3)

	go func() {
		vpcs, err = ec2Client.DescribeVpcs(context.Background(), &ec2.DescribeVpcsInput{})
		errorCh <- err
	}()

	go func() {
		dbs, err := rdsClient.DescribeDBInstances(context.Background(), &rds.DescribeDBInstancesInput{})
		if err != nil {
			errorCh <- err
			return
		}
		result.DBS = dbs.DBInstances
		errorCh <- nil
	}()

	go func() {
		existing, err := cfClient.DescribeStacks(context.Background(), &cloudformation.DescribeStacksInput{
			StackName: aws.String("torpedo"),
		})
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "ValidationError" && apiErr.ErrorMessage() == "Stack with id torpedo does not exist" {
			errorCh <- nil
			return
		}
		if err == nil {
			stack = &existing.Stacks[0]
		}
		errorCh <- err
	}()

	for i := 0; i < cap(errorCh); i++ {
		if err := <-errorCh; err != nil {
			return nil, err
		}
	}

	resources := template["Resources"].(map[string]interface{})
	outputs := template["Outputs"].(map[string]interface{})
	for _, vpc := range vpcs.Vpcs {
		vpcID := strings.ReplaceAll(*vpc.VpcId, "-", "")
		resources["SecurityGroupFor"+vpcID] = map[string]interface{}{
			"Type": "AWS::EC2::SecurityGroup",
			"Properties": map[string]interface{}{
				"GroupName":        "torpedo-container-security-group-vpc-" + *vpc.VpcId,
				"GroupDescription": "torpedo security group for " + *vpc.VpcId,
				"VpcId":            *vpc.VpcId,
				"SecurityGroupIngress": []map[string]interface{}{
					{
						"IpProtocol": -1,
						"CidrIp":     "0.0.0.0/0",
					},
				},
			},
		}
		outputs["SecurityGroup"+vpcID] = map[string]interface{}{
			"Value": map[string]interface{}{"Fn::GetAtt": []string{"SecurityGroupFor" + vpcID,
				"GroupId"}},
		}
	}

	for _, db := range result.DBS {
		for _, sg := range db.VpcSecurityGroups {
			vpc := strings.ReplaceAll(*db.DBSubnetGroup.VpcId, "-", "")
			id := strings.ReplaceAll(*db.DBInstanceIdentifier, "-", "")
			resources["AllowRDS"+id] = map[string]interface{}{
				"Type": "AWS::EC2::SecurityGroupIngress",
				"Properties": map[string]interface{}{
					"Description": "torpedo route to database",
					"GroupId":     *sg.VpcSecurityGroupId,
					"IpProtocol":  -1,
					"SourceSecurityGroupId": map[string]interface{}{
						"Ref": "SecurityGroupFor" + vpc,
					},
				},
			}
		}
	}

	templateBody, err := json.Marshal(template)
	if err != nil {
		return nil, err
	}

	if stack != nil {
		_, err = cfClient.UpdateStack(context.Background(), &cloudformation.UpdateStackInput{
			StackName:    aws.String("torpedo"),
			TemplateBody: aws.String(string(templateBody)),
			Capabilities: []types.Capability{types.CapabilityCapabilityIam},
		})
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "ValidationError" && apiErr.ErrorMessage() == "No updates are to be performed." {
		} else if err != nil {
			return nil, err
		}
	} else {
		_, err = cfClient.CreateStack(context.Background(), &cloudformation.CreateStackInput{
			StackName:    aws.String("torpedo"),
			TemplateBody: aws.String(string(templateBody)),
			Capabilities: []types.Capability{types.CapabilityCapabilityIam},
		})
		if err != nil {
			return nil, err
		}
	}

	for {
		existing, err := cfClient.DescribeStacks(context.Background(), &cloudformation.DescribeStacksInput{
			StackName: aws.String("torpedo"),
		})
		if err != nil {
			return nil, err
		}
		if existing.Stacks[0].StackStatus == types.StackStatusCreateComplete || existing.Stacks[0].StackStatus == types.StackStatusUpdateComplete {
			stack = &existing.Stacks[0]
			break
		}
		if existing.Stacks[0].StackStatus == types.StackStatusCreateFailed || existing.Stacks[0].StackStatus == types.StackStatusUpdateRollbackComplete {
			return nil, errors.New("stack failed to create")
		}
		time.Sleep(1 * time.Second)
	}

	for _, output := range stack.Outputs {
		result.Outputs[*output.OutputKey] = *output.OutputValue
	}

	return result, nil
}

func (b *Bastion) Start(target rdsTypes.DBInstance, publicKey string) (*ecsTypes.Task, string, error) {
	cfg, err := config.LoadDefaultConfig(context.Background())
	ec2Client := ec2.NewFromConfig(cfg)
	ecsClient := ecs.NewFromConfig(cfg)

	describeSubnets, err := ec2Client.DescribeSubnets(context.Background(), &ec2.DescribeSubnetsInput{
		Filters: []ec2Types.Filter{
			{
				Name:   aws.String("vpc-id"),
				Values: []string{*target.DBSubnetGroup.VpcId},
			},
			{
				Name:   aws.String("mapPublicIpOnLaunch"),
				Values: []string{"true"},
			},
		},
	})
	if err != nil {
		return nil, "", err
	}

	subnets := []string{}
	for _, subnet := range describeSubnets.Subnets {

		subnets = append(subnets, *subnet.SubnetId)
	}
	vpcID := strings.ReplaceAll(*target.DBSubnetGroup.VpcId, "-", "")
	securityGroup := b.Outputs["SecurityGroup"+vpcID]
	task, err := ecsClient.RunTask(context.Background(), &ecs.RunTaskInput{
		LaunchType:     ecsTypes.LaunchTypeFargate,
		Cluster:        aws.String("TorpedoCluster"),
		TaskDefinition: aws.String("torpedo-bastion"),
		Overrides: &ecsTypes.TaskOverride{
			ContainerOverrides: []ecsTypes.ContainerOverride{
				{
					Name: aws.String("torpedo-bastion"),
					Environment: []ecsTypes.KeyValuePair{{
						Name:  aws.String("TORPEDO_PUBLIC_KEY"),
						Value: aws.String(publicKey),
					}},
				},
			},
		},
		NetworkConfiguration: &ecsTypes.NetworkConfiguration{
			AwsvpcConfiguration: &ecsTypes.AwsVpcConfiguration{
				SecurityGroups: []string{
					securityGroup,
				},
				Subnets:        subnets,
				AssignPublicIp: ecsTypes.AssignPublicIpEnabled,
			},
		},
	})
	if err != nil {
		return nil, "", err
	}

	for {
		describe, err := ecsClient.DescribeTasks(context.Background(), &ecs.DescribeTasksInput{
			Cluster: aws.String("TorpedoCluster"),
			Tasks:   []string{*task.Tasks[0].TaskArn},
		})
		if err != nil {
			return nil, "", err
		}
		if *describe.Tasks[0].LastStatus == "RUNNING" {
			eni := describe.Tasks[0].Attachments[0].Details[1].Value
			interfaces, err := ec2Client.DescribeNetworkInterfaces(context.Background(), &ec2.DescribeNetworkInterfacesInput{
				NetworkInterfaceIds: []string{*eni},
			})
			publicIP := *interfaces.NetworkInterfaces[0].Association.PublicIp
			if err != nil {
				return nil, "", err
			}
			b.Task = &describe.Tasks[0]
			return &describe.Tasks[0], publicIP, nil
		}
		time.Sleep(1 * time.Second)
	}
}

func (b *Bastion) Shutdown() error {
	slog.Info("bastion shutting down", "task", *b.Task.TaskArn)
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		return err
	}
	ecsClient := ecs.NewFromConfig(cfg)
	_, err = ecsClient.StopTask(context.Background(), &ecs.StopTaskInput{
		Task:    b.Task.TaskArn,
		Cluster: aws.String("TorpedoCluster"),
	})
	if err != nil {
		return err
	}
	return nil
}
