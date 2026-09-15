#!/usr/bin/env bash
set -Eeuo pipefail

readonly profile=${AWS_PROFILE:-hr-portal-friend}
readonly region=${AWS_REGION:-us-east-1}
readonly account_id=740089361110
readonly instance_id=i-086da8aaafa2b2683
readonly stack_name=go-airline-booking-deploy
readonly script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

actual_account=$(aws sts get-caller-identity --profile "$profile" --query Account --output text)
if [[ $actual_account != "$account_id" ]]; then
    echo "Refusing to provision the unexpected AWS account" >&2
    exit 1
fi

aws cloudformation deploy \
    --profile "$profile" \
    --region "$region" \
    --stack-name "$stack_name" \
    --template-file "$script_dir/infrastructure.yml" \
    --capabilities CAPABILITY_NAMED_IAM \
    --no-fail-on-empty-changeset

profile_name=$(aws cloudformation describe-stacks \
    --profile "$profile" --region "$region" --stack-name "$stack_name" \
    --query "Stacks[0].Outputs[?OutputKey=='InstanceProfileName'].OutputValue | [0]" \
    --output text)

association_id=$(aws ec2 describe-iam-instance-profile-associations \
    --profile "$profile" --region "$region" \
    --filters "Name=instance-id,Values=${instance_id}" \
    --query 'IamInstanceProfileAssociations[0].AssociationId' --output text)
if [[ $association_id == None ]]; then
    aws ec2 associate-iam-instance-profile \
        --profile "$profile" --region "$region" --instance-id "$instance_id" \
        --iam-instance-profile "Name=${profile_name}" >/dev/null
else
    associated_arn=$(aws ec2 describe-iam-instance-profile-associations \
        --profile "$profile" --region "$region" \
        --association-ids "$association_id" \
        --query 'IamInstanceProfileAssociations[0].IamInstanceProfile.Arn' --output text)
    if [[ $associated_arn != */$profile_name ]]; then
        echo "Refusing to replace an existing EC2 instance profile" >&2
        exit 1
    fi
fi

aws cloudformation describe-stacks \
    --profile "$profile" --region "$region" --stack-name "$stack_name" \
    --query 'Stacks[0].Outputs' --output table
