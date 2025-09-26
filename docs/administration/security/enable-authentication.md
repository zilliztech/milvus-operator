# Authenticate User Access
This topic describes how to manage user authentication in Milvus.

Milvus supports authentication with username and password.

## Enable authentication in Milvus

You can enable authentication by setting the `spec.common.security.authorizationEnabled` field to `true` in the `Milvus` CRD.


```yaml
apiVersion: milvus.io/v1beta1
kind: Milvus
metadata:
  name: my-release
  labels:
    app: milvus
spec:
  # Omit other fields ...
  config:
    common:
      security:
        authorizationEnabled: true
```

## Create an authenticated user
    

A root user (password: <code>Milvus</code>) is created along with each Milvus instance by default. It is recommended to change the password of the root user when you start Milvus for the first time. The root user can be used to create new users for authenticated access.

Create a user with username and password with the following command.

```python
from pymilvus import utility
utility.create_user('user', 'password', using='default') 
```

| Parameter                    |  Description                                    |
| ---------------------------- | ----------------------------------------------- |
| <code>user</code>            | Username to create.                             |
| <code>password</code>        | Password for the user to create.                |
| <code>using</code>           | Alias of the Milvus server to create the user.  |

    
## Connect Milvus with an authenticated user

Connect Milvus with an existing user.

```python
from pymilvus import connections
connections.connect(
    alias='default',
    host='localhost',
    port='19530',
    user='user',
    password='password',
)
```

| Parameter                      |  Description                                |
| ------------------------------ | ------------------------------------------- |
| <code>alias</code>             | Alias of the Milvus server to connect.      |
| <code>host</code>              | IP address of the Milvus server to connect. |
| <code>port</code>              | Port of the Milvus server to connect.       |
| <code>user</code>              | Username used to connect.                   |
| <code>password</code>          | Password used to connect.                   |

<div class="alert note">
To stop using the authenticated access, or to log in to another authenticated user, you need to disconnect from the Milvus instance and re-connect to it.
</div>

## Reset password

Change the password for an existing user and reset the Milvus connection.

```python
from pymilvus import utility
utility.reset_password('user', 'old_password', 'new_password', using='default')
```

| Parameter                    |  Description                            |
| ---------------------------- | --------------------------------------- |
| <code>user</code>            | Username to reset password.             |
| <code>password</code>        | New password for the user.              |
| <code>using</code>           | Alias of the Milvus server.             |

## Delete a user

Delete an authenticated user.

```python
from pymilvus import utility
utility.delete_user('user', using='default')
```

| Parameter                    |  Description                            |
| ---------------------------- | --------------------------------------- |
| <code>user</code>            | Username to delete.                     |
| <code>using</code>           | Alias of the Milvus server.             |

## List all users

List all the credential users.

```python
from pymilvus import utility
users = utility.list_usernames(using='default')
```

## Limitations

1. Username must not be empty, and must not exceed 32 characters in length. It must start with a letter, and only contains underscores, letters, or numbers.
2. Password must have at least 6 characters and must not exceed 256 characters in length.
