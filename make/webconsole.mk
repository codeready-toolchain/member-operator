WEBCONSOLE_SRC_DIR = $(PWD)/webconsole_source
DIST_DIR = $(WEBCONSOLE_SRC_DIR)/dist

.PHONY: generate-webconsole-scripts
generate-webconsole-scripts:
	mkdir -p $(DIST_DIR)
	#rm -f $(DIST_DIR)/*
	cd $(WEBCONSOLE_SRC_DIR);${IMAGE_BUILDER} build --volume $(DIST_DIR):/dist:z,U -f $(WEBCONSOLE_SRC_DIR)/Containerfile .