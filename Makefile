.PHONY: test verify generate manifests install kind-test
test:
	go test ./...
	node --test internal/server/ui_test.js
verify:
	gofmt -w $$(find api cmd internal -name '*.go')
	go test ./...
	node --test internal/server/ui_test.js
	go vet ./...
	git diff --check
generate:
	go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.19.0 object paths=./api/...
manifests:
	go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.19.0 crd paths=./api/... output:crd:artifacts:config=config/crd
install:
	kubectl apply -k config
kind-test:
	./hack/kind-test.sh
