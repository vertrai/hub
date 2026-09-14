# Hub Domain

## Language

**Gateway User**: An identity that groups one or more Access Keys. A Gateway User does not directly own runtime resources.

_Avoid_: treating a Gateway User as the resource allocation boundary.

**Access Key**: A credential issued to a Gateway User and the allocation identity for gateway resources. Each Access Key owns at most one Browser, at most one Google Account, and at most one automatically allocated LLM Key.

_Avoid_: platform token, user token.

**Browser**: A persistent browser allocation, including its provider session and profile, owned by one Access Key.

**Google Account**: A managed Google Workspace account allocated from the account pool and owned by one Access Key after assignment.

**Google Account Pool**: The set of managed Google Accounts that are available for assignment or already assigned to Access Keys.


**LLM Key**: A Manager resource credential with an explicit allowed-model list and default model. Automatically allocated keys belong to one Access Key; administrators may also create standalone keys.

**LLM Provider**: An upstream account or API configuration identified by an automatically generated internal ID.

**Model Route**: A public model ID mapped to one Provider and its upstream model ID. Clients use public model IDs without Provider ID prefixes.

**hub-chat**: A reserved model alias resolved to the requesting LLM Key’s current default model on every request. Changing that default changes subsequent inference without reconfiguring the agent.

**Automatic LLM Allocation Settings**: The public relay URL and initial model policy for newly allocated keys. Existing keys retain their individual model policies; administrators update them explicitly.


**Google User**: A Manager user authenticated by a verified Google subject. Its stable ID is `google_<subject>`. Google login is shared by the user portal and administration; admin permissions are checked separately against the configured email allowlist. Google and WeChat identities are not merged by email.

**Web Agent**: A durable commercial instance owned by a User. It retains an Access Key and a Pod across subscription suspension. Its desired entitlement state is separate from deployment progress. Interrupted non-idempotent deployment operations require reconciliation.

**Invite Code**: A single-use grant of one Web Agent, optionally restricted to a product and expiry. Redemption and instance reservation commit together.

**Billing**: A Manager order associated with a Stripe subscription and a Web Agent. A verified webhook updates billing and desired instance state transactionally; deployment runs separately. Product configuration maps the website product identifier to a catalog entry and Stripe Price ID.
