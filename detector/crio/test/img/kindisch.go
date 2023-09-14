// Copyright 2023 Harald Albrecht.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package img

// Name of the kindisch image that bases on "kindest/base" and comes with the
// two CRI supporting engines, “containerd” and “cri-o”. This also
// containers a very rudimentary CNI networking configuration.
const Name = "thediveo/kindisch-ww-containerd-crio"
