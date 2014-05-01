package stager_test

import (
	"time"

	Bbs "github.com/cloudfoundry-incubator/runtime-schema/bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	. "github.com/cloudfoundry-incubator/stager/stager"
	"github.com/cloudfoundry/gunk/timeprovider"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Stage", func() {
	var stager Stager
	var bbs *Bbs.BBS

	BeforeEach(func() {
		bbs = Bbs.New(etcdRunner.Adapter(), timeprovider.NewTimeProvider())
		compilers := map[string]string{
			"penguin":     "penguin-compiler",
			"rabbit_hole": "rabbit-hole-compiler",
		}
		stager = New(bbs, compilers)
	})

	Context("when file the server is available", func() {
		BeforeEach(func() {
			_, _, err := bbs.MaintainFileServerPresence(10*time.Second, "http://file-server.com/", "abc123")
			Ω(err).ShouldNot(HaveOccurred())
		})

		It("creates a Task with staging instructions", func() {
			modelChannel, _, _ := bbs.WatchForDesiredTask()

			err := stager.Stage(models.StagingRequestFromCC{
				AppId:                          "bunny",
				TaskId:                         "hop",
				AppBitsDownloadUri:             "http://example-uri.com/bunny",
				BuildArtifactsCacheDownloadUri: "http://example-uri.com/bunny-droppings",
				Stack:           "rabbit_hole",
				FileDescriptors: 17,
				MemoryMB:        256,
				DiskMB:          1024,
				Buildpacks: []models.Buildpack{
					{Key: "zfirst-buildpack", Url: "first-buildpack-url"},
					{Key: "asecond-buildpack", Url: "second-buildpack-url"},
				},
				Environment: []models.EnvironmentVariable{
					{"VCAP_APPLICATION", "foo"},
					{"VCAP_SERVICES", "bar"},
				},
			})
			Ω(err).ShouldNot(HaveOccurred())

			var task *models.Task
			Eventually(modelChannel).Should(Receive(&task))

			Ω(task.Guid).To(Equal("bunny-hop"))
			Ω(task.Stack).To(Equal("rabbit_hole"))
			Ω(task.Log.Guid).To(Equal("bunny"))
			Ω(task.Log.SourceName).To(Equal("STG"))
			Ω(task.FileDescriptors).To(Equal(17))
			Ω(task.Log.Index).To(BeNil())

			expectedActions := []models.ExecutorAction{
				models.EmitProgressFor(
					models.ExecutorAction{
						models.DownloadAction{
							From:    "http://file-server.com/v1/static/rabbit-hole-compiler",
							To:      "/tmp/compiler",
							Extract: true,
						},
					},
					"",
					"",
					"Failed to Download Smelter",
				),
				models.EmitProgressFor(
					models.ExecutorAction{
						models.DownloadAction{
							From:    "http://example-uri.com/bunny",
							To:      "/app",
							Extract: true,
						},
					},
					"Downloading App Package",
					"Downloaded App Package",
					"Failed to Download App Package",
				),
				models.EmitProgressFor(
					models.ExecutorAction{
						models.DownloadAction{
							From:    "first-buildpack-url",
							To:      "/tmp/buildpacks/zfirst-buildpack",
							Extract: true,
						},
					},
					"Downloading Buildpack",
					"Downloaded Buildpack",
					"Failed to Download Buildpack",
				),
				models.EmitProgressFor(
					models.ExecutorAction{
						models.DownloadAction{
							From:    "second-buildpack-url",
							To:      "/tmp/buildpacks/asecond-buildpack",
							Extract: true,
						},
					},
					"Downloading Buildpack",
					"Downloaded Buildpack",
					"Failed to Download Buildpack",
				),
				models.Try(
					models.EmitProgressFor(
						models.ExecutorAction{
							models.DownloadAction{
								From:    "http://example-uri.com/bunny-droppings",
								To:      "/tmp/cache",
								Extract: true,
							},
						},
						"Downloading Build Artifacts Cache",
						"Downloaded Build Artifacts Cache",
						"No Build Artifacts Cache Found.  Proceeding...",
					),
				),
				models.EmitProgressFor(
					models.ExecutorAction{
						models.RunAction{
							Script: "/tmp/compiler/run" +
								" -appDir='/app'" +
								" -buildArtifactsCacheDir='/tmp/cache'" +
								" -buildpackOrder='zfirst-buildpack,asecond-buildpack'" +
								" -buildpacksDir='/tmp/buildpacks'" +
								" -outputDir='/tmp/droplet'" +
								" -resultDir='/tmp/result'",
							Env: []models.EnvironmentVariable{
								{"VCAP_APPLICATION", "foo"},
								{"VCAP_SERVICES", "bar"},
							},
							Timeout: 15 * time.Minute,
						},
					},
					"Staging...",
					"Staging Complete",
					"Staging Failed",
				),
				models.EmitProgressFor(
					models.ExecutorAction{
						models.UploadAction{
							From:     "/tmp/droplet/",
							To:       "http://file-server.com/v1/droplet/bunny",
							Compress: false,
						},
					},
					"Uploading Droplet",
					"Droplet Uploaded",
					"Failed to Upload Droplet",
				),
				models.Try(
					models.EmitProgressFor(
						models.ExecutorAction{
							models.UploadAction{
								From:     "/tmp/cache/",
								To:       "http://file-server.com/v1/build_artifacts/bunny",
								Compress: true,
							},
						},
						"Uploading Build Artifacts Cache",
						"Uploaded Build Artifacts Cache",
						"Failed to Upload Build Artifacts Cache.  Proceeding...",
					),
				),
				models.EmitProgressFor(
					models.ExecutorAction{
						models.FetchResultAction{
							File: "/tmp/result/result.json",
						},
					},
					"",
					"",
					"Failed to Fetch Detected Buildpack",
				),
			}

			for i, action := range task.Actions {
				Ω(action).To(Equal(expectedActions[i]))
			}

			Ω(task.MemoryMB).To(Equal(256))
			Ω(task.DiskMB).To(Equal(1024))
		})

	})

	Context("when build artifacts download uris are not provided", func() {
		BeforeEach(func() {
			_, _, err := bbs.MaintainFileServerPresence(10*time.Second, "http://file-server.com/", "abc123")
			Ω(err).ShouldNot(HaveOccurred())
		})

		It("does not instruct the executor to download the cache", func() {
			modelChannel, _, _ := bbs.WatchForDesiredTask()

			err := stager.Stage(models.StagingRequestFromCC{
				AppId:              "bunny",
				TaskId:             "hop",
				AppBitsDownloadUri: "http://example-uri.com/bunny",
				Stack:              "rabbit_hole",
				FileDescriptors:    17,
				MemoryMB:           256,
				DiskMB:             1024,
				Buildpacks: []models.Buildpack{
					{Key: "zfirst-buildpack", Url: "first-buildpack-url"},
					{Key: "asecond-buildpack", Url: "second-buildpack-url"},
				},
				Environment: []models.EnvironmentVariable{
					{"VCAP_APPLICATION", "foo"},
					{"VCAP_SERVICES", "bar"},
				},
			})
			Ω(err).ShouldNot(HaveOccurred())

			var task *models.Task
			Eventually(modelChannel).Should(Receive(&task))

			Ω(task.Guid).To(Equal("bunny-hop"))
			Ω(task.Stack).To(Equal("rabbit_hole"))
			Ω(task.Log.Guid).To(Equal("bunny"))
			Ω(task.Log.SourceName).To(Equal("STG"))
			Ω(task.FileDescriptors).To(Equal(17))
			Ω(task.Log.Index).To(BeNil())

			expectedActions := []models.ExecutorAction{
				models.EmitProgressFor(
					models.ExecutorAction{
						models.DownloadAction{
							From:    "http://file-server.com/v1/static/rabbit-hole-compiler",
							To:      "/tmp/compiler",
							Extract: true,
						},
					},
					"",
					"",
					"Failed to Download Smelter",
				),
				models.EmitProgressFor(
					models.ExecutorAction{
						models.DownloadAction{
							From:    "http://example-uri.com/bunny",
							To:      "/app",
							Extract: true,
						},
					},
					"Downloading App Package",
					"Downloaded App Package",
					"Failed to Download App Package",
				),
				models.EmitProgressFor(
					models.ExecutorAction{
						models.DownloadAction{
							From:    "first-buildpack-url",
							To:      "/tmp/buildpacks/zfirst-buildpack",
							Extract: true,
						},
					},
					"Downloading Buildpack",
					"Downloaded Buildpack",
					"Failed to Download Buildpack",
				),
				models.EmitProgressFor(
					models.ExecutorAction{
						models.DownloadAction{
							From:    "second-buildpack-url",
							To:      "/tmp/buildpacks/asecond-buildpack",
							Extract: true,
						},
					},
					"Downloading Buildpack",
					"Downloaded Buildpack",
					"Failed to Download Buildpack",
				),
				models.EmitProgressFor(
					models.ExecutorAction{
						models.RunAction{
							Script: "/tmp/compiler/run" +
								" -appDir='/app'" +
								" -buildArtifactsCacheDir='/tmp/cache'" +
								" -buildpackOrder='zfirst-buildpack,asecond-buildpack'" +
								" -buildpacksDir='/tmp/buildpacks'" +
								" -outputDir='/tmp/droplet'" +
								" -resultDir='/tmp/result'",
							Env: []models.EnvironmentVariable{
								{"VCAP_APPLICATION", "foo"},
								{"VCAP_SERVICES", "bar"},
							},
							Timeout: 15 * time.Minute,
						},
					},
					"Staging...",
					"Staging Complete",
					"Staging Failed",
				),
				models.EmitProgressFor(
					models.ExecutorAction{
						models.UploadAction{
							From:     "/tmp/droplet/",
							To:       "http://file-server.com/v1/droplet/bunny",
							Compress: false,
						},
					},
					"Uploading Droplet",
					"Droplet Uploaded",
					"Failed to Upload Droplet",
				),
				models.Try(
					models.EmitProgressFor(
						models.ExecutorAction{
							models.UploadAction{
								From:     "/tmp/cache/",
								To:       "http://file-server.com/v1/build_artifacts/bunny",
								Compress: true,
							},
						},
						"Uploading Build Artifacts Cache",
						"Uploaded Build Artifacts Cache",
						"Failed to Upload Build Artifacts Cache.  Proceeding...",
					),
				),
				models.EmitProgressFor(
					models.ExecutorAction{
						models.FetchResultAction{
							File: "/tmp/result/result.json",
						},
					},
					"",
					"",
					"Failed to Fetch Detected Buildpack",
				),
			}

			for i, action := range task.Actions {
				Ω(action).To(Equal(expectedActions[i]))
			}

			Ω(task.MemoryMB).To(Equal(256))
			Ω(task.DiskMB).To(Equal(1024))
		})
	})

	Context("when build artifacts download url is not a valid url", func() {
		It("return a url parsing error", func() {
			err := stager.Stage(models.StagingRequestFromCC{
				AppId:                          "bunny",
				TaskId:                         "hop",
				AppBitsDownloadUri:             "http://example-uri.com/bunny",
				BuildArtifactsCacheDownloadUri: "not-a-url",
				Stack:           "rabbit_hole",
				FileDescriptors: 17,
				MemoryMB:        256,
				DiskMB:          1024,
				Buildpacks: []models.Buildpack{
					{Key: "zfirst-buildpack", Url: "first-buildpack-url"},
					{Key: "asecond-buildpack", Url: "second-buildpack-url"},
				},
				Environment: []models.EnvironmentVariable{
					{"VCAP_APPLICATION", "foo"},
					{"VCAP_SERVICES", "bar"},
				},
			})
			Ω(err).Should(HaveOccurred())
		})
	})

	Context("when file server is not available", func() {
		It("should return an error", func() {
			err := stager.Stage(models.StagingRequestFromCC{
				AppId:                          "bunny",
				TaskId:                         "hop",
				AppBitsDownloadUri:             "http://example-uri.com/bunny",
				BuildArtifactsCacheDownloadUri: "http://example-uri.com/bunny-droppings",
				Stack:    "rabbit_hole",
				MemoryMB: 256,
				DiskMB:   1024,
			})

			Ω(err).Should(HaveOccurred())
			Ω(err.Error()).Should(Equal("no available file server present"))
		})
	})

	Context("when no compiler is defined for the requested stack in stager configuration", func() {
		BeforeEach(func() {
			_, _, err := bbs.MaintainFileServerPresence(10*time.Second, "http://file-server.com/", "abc123")
			Ω(err).ShouldNot(HaveOccurred())
		})

		It("should return an error", func() {
			bbs.WatchForDesiredTask()

			err := stager.Stage(models.StagingRequestFromCC{
				AppId:                          "bunny",
				TaskId:                         "hop",
				AppBitsDownloadUri:             "http://example-uri.com/bunny",
				BuildArtifactsCacheDownloadUri: "http://example-uri.com/bunny-droppings",
				Stack:    "no_such_stack",
				MemoryMB: 256,
				DiskMB:   1024,
			})

			Ω(err).Should(HaveOccurred())
			Ω(err.Error()).Should(Equal("no compiler defined for requested stack"))
		})
	})
})
