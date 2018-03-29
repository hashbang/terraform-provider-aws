package aws

import (
	"crypto/rand"
	"fmt"
	"log"
	"math/big"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/hashicorp/errwrap"
	"github.com/hashicorp/terraform/helper/encryption"
	"github.com/hashicorp/terraform/helper/schema"
	"github.com/hashicorp/terraform/helper/validation"
)

func resourceAwsIamUserLoginProfile() *schema.Resource {
	return &schema.Resource{
		Create: resourceAwsIamUserLoginProfileCreate,
		Read:   schema.Noop,
		Update: schema.Noop,
		Delete: schema.RemoveFromState,

		Schema: map[string]*schema.Schema{
			"user": {
				Type:     schema.TypeString,
				Required: true,
			},
			"pgp_key": {
				Type:     schema.TypeString,
				Required: true,
			},
			"password_reset_required": {
				Type:     schema.TypeBool,
				Optional: true,
				Default:  true,
			},
			"password_length": {
				Type:         schema.TypeInt,
				Optional:     true,
				Default:      20,
				ValidateFunc: validation.IntBetween(4, 128),
			},

			"key_fingerprint": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"encrypted_password": {
				Type:     schema.TypeString,
				Computed: true,
			},
		},
	}
}

// generatePassword generates a random password of a given length using
// characters that are likely to satisfy any possible AWS password policy
// (given sufficient length).
func generatePassword(length int) string {
	charset := "abcdefghijklmnopqrstuvwxyz" +
		"ABCDEFGHIJKLMNOPQRSTUVWXYZ" +
		"0123456789" +
		"!@#$%^&*()_+-=[]{}|'"

	result := make([]byte, length)
	charsetSize := big.NewInt(int64(len(charset)))

	for i := range result {
		r, err := rand.Int(rand.Reader, charsetSize)
		if err != nil {
			panic(err)
		}
		if !r.IsInt64() {
			panic("rand.Int() not representable as an Int64")
		}

		result[i] = charset[r.Int64()]
	}

	return string(result)
}

func resourceAwsIamUserLoginProfileCreate(d *schema.ResourceData, meta interface{}) error {
	iamconn := meta.(*AWSClient).iamconn

	encryptionKey, err := encryption.RetrieveGPGKey(d.Get("pgp_key").(string))
	if err != nil {
		return err
	}

	username := d.Get("user").(string)
	passwordResetRequired := d.Get("password_reset_required").(bool)
	passwordLength := d.Get("password_length").(int)

	_, err = iamconn.GetLoginProfile(&iam.GetLoginProfileInput{
		UserName: aws.String(username),
	})
	if err != nil {
		if awsErr, ok := err.(awserr.Error); ok && awsErr.Code() != "NoSuchEntity" {
			// If there is already a login profile, bring it under management (to prevent
			// resource creation diffs) - we will never modify it, but obviously cannot
			// set the password.
			d.SetId(username)
			d.Set("key_fingerprint", "")
			d.Set("encrypted_password", "")
			return nil
		}
	}

	initialPassword := generatePassword(passwordLength)
	fingerprint, encrypted, err := encryption.EncryptValue(encryptionKey, initialPassword, "Password")
	if err != nil {
		return err
	}

	request := &iam.CreateLoginProfileInput{
		UserName:              aws.String(username),
		Password:              aws.String(initialPassword),
		PasswordResetRequired: aws.Bool(passwordResetRequired),
	}

	log.Println("[DEBUG] Create IAM User Login Profile request:", request)
	createResp, err := iamconn.CreateLoginProfile(request)
	if err != nil {
		if awsErr, ok := err.(awserr.Error); ok && awsErr.Code() == "EntityAlreadyExists" {
			// If there is already a login profile, bring it under management (to prevent
			// resource creation diffs) - we will never modify it, but obviously cannot
			// set the password.
			d.SetId(username)
			d.Set("key_fingerprint", "")
			d.Set("encrypted_password", "")
			return nil
		}
		return errwrap.Wrapf(fmt.Sprintf("Error creating IAM User Login Profile for %q: {{err}}", username), err)
	}

	d.SetId(*createResp.LoginProfile.UserName)
	d.Set("key_fingerprint", fingerprint)
	d.Set("encrypted_password", encrypted)
	return nil
}
