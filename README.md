# RBAC Controller


## Components used in setting up this project
```
operator-sdk 1.26.0
go 1.19.5
```

## Commands
```
Create kind cluster:
make kind_create

Destroy kind cluster:
make kind_destroy

Create 
operator-sdk create api --group rbac.pliu.github.com --version v1alpha1 --kind PermissionedRole --resource --controller
```
